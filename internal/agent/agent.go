package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/JR-G/rook/internal/memory"
	"github.com/JR-G/rook/internal/ollama"
	"github.com/JR-G/rook/internal/output"
	"github.com/JR-G/rook/internal/persona"
	"github.com/JR-G/rook/internal/tools/web"
)

// Config contains the runtime settings used by the agent loop.
type Config struct {
	ChatModel          string
	EmbeddingModel     string
	Temperature        float64
	MinWriteImportance float64
	MaxPromptItems     int
	MaxEpisodeItems    int
	WebEnabled         bool
	WebMaxResults      int
	AutoOnFreshness    bool
}

// Request is an inbound conversational turn.
type Request struct {
	ChannelID string
	ThreadTS  string
	UserID    string
	Text      string
}

// Response is the Slack-safe output plus internal metadata.
type Response struct {
	Text        string
	UsedWeb     bool
	WebProvider string
}

// Service orchestrates memory, persona, tools, and local inference.
type Service struct {
	ollama    *ollama.Client
	store     *memory.Store
	persona   *persona.Manager
	searcher  web.Searcher
	filter    output.Filter
	extractor memory.Extractor

	mu     sync.RWMutex
	config Config
}

// New creates a new agent service.
func New(
	ollamaClient *ollama.Client,
	store *memory.Store,
	personaManager *persona.Manager,
	searcher web.Searcher,
	filter output.Filter,
	cfg Config,
) *Service {
	return &Service{
		ollama:    ollamaClient,
		store:     store,
		persona:   personaManager,
		searcher:  searcher,
		filter:    filter,
		extractor: memory.Extractor{},
		config:    cfg,
	}
}

// SetChatModel updates the current chat model without a restart.
func (s *Service) SetChatModel(model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.ChatModel = strings.TrimSpace(model)
}

// ChatModel returns the current runtime chat model.
func (s *Service) ChatModel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.config.ChatModel
}

// EmbeddingModel returns the current embedding model.
func (s *Service) EmbeddingModel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.config.EmbeddingModel
}

// UpdateConfig replaces the agent's runtime settings and tool provider.
func (s *Service) UpdateConfig(cfg Config, searcher web.Searcher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = cfg
	s.searcher = searcher
}

// Respond handles a general conversational turn end-to-end.
func (s *Service) Respond(ctx context.Context, request Request) (Response, error) {
	cfg := s.snapshot()
	queryEmbedding, _ := s.ollama.Embed(ctx, cfg.EmbeddingModel, request.Text)

	if _, err := s.store.RecordEpisode(ctx, memory.EpisodeInput{
		ChannelID: request.ChannelID,
		ThreadTS:  request.ThreadTS,
		UserID:    request.UserID,
		Role:      "user",
		Source:    "user",
		Text:      request.Text,
	}); err != nil {
		return Response{}, err
	}

	retrieval, err := s.store.SearchContext(ctx, request.Text, queryEmbedding, memory.RetrievalLimits{
		MaxPromptItems:  cfg.MaxPromptItems,
		MaxEpisodeItems: cfg.MaxEpisodeItems,
	})
	if err != nil {
		return Response{}, err
	}

	var (
		searchResults []web.Result
		usedWeb       bool
	)
	if s.shouldUseWeb(cfg, request.Text) && s.searcher.Enabled() {
		searchResults, err = s.searcher.Search(ctx, request.Text, cfg.WebMaxResults)
		if err == nil && len(searchResults) > 0 {
			usedWeb = true
		}
	}

	systemPrompt, err := s.persona.RenderSystemPrompt(ctx)
	if err != nil {
		return Response{}, err
	}

	userPrompt := buildUserPrompt(request.Text, retrieval, searchResults, usedWeb)
	result, err := s.ollama.Chat(ctx, cfg.ChatModel, []ollama.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, cfg.Temperature)
	if err != nil {
		return Response{}, err
	}

	reply := s.filter.Clean(result.Content)
	if usedWeb && !strings.Contains(strings.ToLower(reply), "live web lookup") {
		reply = fmt.Sprintf("%s\n\nLive web lookup used.", reply)
	}

	if _, err := s.store.RecordEpisode(ctx, memory.EpisodeInput{
		ChannelID: request.ChannelID,
		ThreadTS:  request.ThreadTS,
		UserID:    "rook",
		Role:      "assistant",
		Source:    "assistant",
		Text:      reply,
	}); err != nil {
		return Response{}, err
	}

	for _, candidate := range s.extractor.Candidates(memory.Interaction{
		UserText:      request.Text,
		AssistantText: reply,
	}) {
		if candidate.Importance < cfg.MinWriteImportance {
			continue
		}
		if len(candidate.Embedding) == 0 {
			embedding, embedErr := s.ollama.Embed(ctx, cfg.EmbeddingModel, candidate.Body)
			if embedErr == nil {
				candidate.Embedding = embedding
			}
		}
		if _, upsertErr := s.store.UpsertMemory(ctx, candidate); upsertErr != nil {
			return Response{}, upsertErr
		}
	}

	if err := s.store.Decay(ctx); err != nil {
		return Response{}, err
	}
	if err := s.persona.ConsolidateIfDue(ctx); err != nil {
		return Response{}, err
	}

	return Response{
		Text:        reply,
		UsedWeb:     usedWeb,
		WebProvider: s.searcher.Provider(),
	}, nil
}

func (s *Service) snapshot() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.config
}

func (s *Service) shouldUseWeb(cfg Config, text string) bool {
	if !cfg.WebEnabled {
		return false
	}

	lowerText := strings.ToLower(text)
	explicitTriggers := []string{
		"look up",
		"search the web",
		"search web",
		"check online",
		"latest",
		"current",
		"today",
		"recent",
		"news",
		"price",
		"weather",
		"release",
		"version",
	}
	for _, trigger := range explicitTriggers {
		if strings.Contains(lowerText, trigger) {
			return true
		}
	}

	return cfg.AutoOnFreshness && strings.Contains(lowerText, "update")
}

func buildUserPrompt(query string, retrieval memory.RetrievalContext, searchResults []web.Result, usedWeb bool) string {
	var builder strings.Builder
	builder.WriteString("User request:\n")
	builder.WriteString(query)
	builder.WriteString("\n\nRelevant memory:\n")
	builder.WriteString(renderMemoryContext(retrieval))

	if usedWeb {
		builder.WriteString("\n\nLive web results:\n")
		builder.WriteString(web.FormatForPrompt(searchResults))
		builder.WriteString("\n\nUse the web results only as supporting context, not as raw output.")
	}

	return builder.String()
}

func renderMemoryContext(retrieval memory.RetrievalContext) string {
	var builder strings.Builder
	builder.WriteString("User facts:\n")
	builder.WriteString(renderItems(retrieval.UserFacts))
	builder.WriteString("\n\nWorking context:\n")
	builder.WriteString(renderItems(retrieval.WorkingContext))
	builder.WriteString("\n\nHistorical episodes:\n")
	builder.WriteString(renderEpisodes(retrieval.Episodes))
	if len(retrieval.Squad0Episodes) > 0 {
		builder.WriteString("\n\nRecent squad0 context:\n")
		builder.WriteString(renderEpisodes(retrieval.Squad0Episodes))
	}

	return builder.String()
}

func renderItems(items []memory.Item) string {
	if len(items) == 0 {
		return "- none"
	}

	var lines []string
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- [%s] %s", item.Type, item.Body))
	}

	return strings.Join(lines, "\n")
}

func renderEpisodes(episodes []memory.Episode) string {
	if len(episodes) == 0 {
		return "- none"
	}

	var lines []string
	for _, episode := range episodes {
		lines = append(lines, fmt.Sprintf("- [%s] %s", episode.Source, episode.Summary))
	}

	return strings.Join(lines, "\n")
}
