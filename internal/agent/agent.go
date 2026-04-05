package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/JR-G/rook/internal/failures"
	"github.com/JR-G/rook/internal/memory"
	"github.com/JR-G/rook/internal/ollama"
	"github.com/JR-G/rook/internal/output"
	"github.com/JR-G/rook/internal/persona"
	"github.com/JR-G/rook/internal/tools/web"
)

// Config contains the runtime settings used by the agent loop.
type Config struct {
	ChatModel          string
	ChatFallbacks      []string
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

const (
	noContext     = "- none"
	roleUser      = "user"
	roleAssistant = "assistant"
)

// Service orchestrates memory, persona, tools, and local inference.
type Service struct {
	ollama    *ollama.Client
	store     *memory.Store
	persona   *persona.Manager
	searcher  web.Searcher
	fetcher   *web.Fetcher
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
	fetcher *web.Fetcher,
	cfg Config,
) *Service {
	return &Service{
		ollama:    ollamaClient,
		store:     store,
		persona:   personaManager,
		searcher:  searcher,
		fetcher:   fetcher,
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
		Role:      roleUser,
		Source:    roleUser,
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
	threadEpisodes, err := s.store.ThreadEpisodes(ctx, request.ChannelID, request.ThreadTS, 4)
	if err != nil {
		return Response{}, err
	}
	threadEpisodes = trimCurrentUserEcho(request.Text, threadEpisodes)

	runtimeState, err := s.memoryStateText(ctx)
	if err != nil {
		return Response{}, err
	}

	var (
		searchResults []web.Result
		usedWeb       bool
	)
	searchResults, usedWeb = s.webContext(ctx, cfg, request.Text)
	fetchedContent := s.fetchURLs(ctx, request.Text)

	systemPrompt, err := s.persona.RenderSystemPrompt(ctx)
	if err != nil {
		return Response{}, err
	}

	retrieval = adjustRetrievalForQuery(request.ChannelID, request.ThreadTS, threadEpisodes, retrieval)
	userPrompt := buildUserPrompt(request.Text, retrieval, threadEpisodes, runtimeState, searchResults, usedWeb, fetchedContent)
	messages := buildChatMessages(systemPrompt, userPrompt, threadEpisodes)
	result, err := s.chatWithFallback(ctx, cfg, messages, output.AnswerSchema())
	if err != nil {
		return Response{}, err
	}

	reply, err := output.ParseAnswer(result.Content)
	if err != nil {
		return Response{}, failures.Wrap(err, "The local model returned an invalid reply shape. Try again.")
	}
	reply, err = s.repairRepeatedThreadReply(ctx, cfg, systemPrompt, userPrompt, threadEpisodes, reply)
	if err != nil {
		return Response{}, err
	}
	if usedWeb && !strings.Contains(strings.ToLower(reply), "live web lookup") {
		reply = fmt.Sprintf("%s\n\nLive web lookup used.", reply)
	}

	if _, err := s.store.RecordEpisode(ctx, memory.EpisodeInput{
		ChannelID: request.ChannelID,
		ThreadTS:  request.ThreadTS,
		UserID:    "rook",
		Role:      roleAssistant,
		Source:    roleAssistant,
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
		if err := s.persistCandidate(ctx, cfg, candidate); err != nil {
			return Response{}, err
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

func (s *Service) memoryStateText(ctx context.Context) (string, error) {
	health, err := s.store.Health(ctx)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"- local memory db healthy: %t\n- durable memory items: %d\n- stored episodes: %d\n- pending reminders: %d",
		health.Reachable,
		health.MemoryCount,
		health.EpisodeCount,
		health.PendingReminds,
	), nil
}

func (s *Service) persistCandidate(ctx context.Context, cfg Config, candidate memory.Candidate) error {
	enriched := candidate
	if len(enriched.Embedding) != 0 {
		_, err := s.store.UpsertMemory(ctx, enriched)

		return err
	}

	embedding, err := s.ollama.Embed(ctx, cfg.EmbeddingModel, enriched.Body)
	if err == nil {
		enriched.Embedding = embedding
	}

	_, err = s.store.UpsertMemory(ctx, enriched)

	return err
}

func (s *Service) webContext(ctx context.Context, cfg Config, text string) ([]web.Result, bool) {
	if !s.shouldUseWeb(cfg, text) || !s.searcher.Enabled() {
		return nil, false
	}

	results, err := s.searcher.Search(ctx, text, cfg.WebMaxResults)
	if err != nil || len(results) == 0 {
		return nil, false
	}

	return results, true
}

func (s *Service) chatWithFallback(
	ctx context.Context,
	cfg Config,
	messages []ollama.Message,
	format any,
) (ollama.ChatResult, error) {
	models := candidateModels(cfg.ChatModel, cfg.ChatFallbacks)
	var lastErr error
	for _, model := range models {
		result, err := s.ollama.ChatStructured(ctx, model, messages, cfg.Temperature, format)
		if err == nil {
			return result, nil
		}
		if !ollama.ShouldFallbackModel(err) {
			return ollama.ChatResult{}, err
		}

		lastErr = err
	}

	if lastErr != nil {
		return ollama.ChatResult{}, lastErr
	}

	return ollama.ChatResult{}, fmt.Errorf("no chat model configured")
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

func (s *Service) fetchURLs(ctx context.Context, text string) []fetchedURL {
	if s.fetcher == nil {
		return nil
	}

	urls := web.ExtractURLs(text)
	if len(urls) == 0 {
		return nil
	}

	fetched := make([]fetchedURL, 0, len(urls))
	for _, rawURL := range urls {
		content, err := s.fetcher.Fetch(ctx, rawURL)
		if err != nil || content == "" {
			continue
		}
		fetched = append(fetched, fetchedURL{URL: rawURL, Content: content})
	}

	return fetched
}

func candidateModels(primary string, fallbacks []string) []string {
	trimmedPrimary := strings.TrimSpace(primary)
	if trimmedPrimary == "" {
		return nil
	}

	models := make([]string, 0, len(fallbacks)+1)
	models = append(models, trimmedPrimary)
	seen := map[string]struct{}{
		strings.ToLower(trimmedPrimary): {},
	}

	for _, fallback := range fallbacks {
		trimmed := strings.TrimSpace(fallback)
		if trimmed == "" {
			continue
		}

		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		models = append(models, trimmed)
	}

	return models
}
