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
	cfg Config,
) *Service {
	return &Service{
		ollama:    ollamaClient,
		store:     store,
		persona:   personaManager,
		searcher:  searcher,
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
	profile := analyseQuery(request.Text, threadEpisodes)

	runtimeState, err := s.memoryStateText(ctx)
	if err != nil {
		return Response{}, err
	}

	var (
		searchResults []web.Result
		usedWeb       bool
	)
	searchResults, usedWeb = s.webContext(ctx, cfg, request.Text)

	systemPrompt, err := s.persona.RenderSystemPrompt(ctx)
	if err != nil {
		return Response{}, err
	}

	retrieval = adjustRetrievalForQuery(request.Text, request.ChannelID, request.ThreadTS, threadEpisodes, retrieval)
	userPrompt := buildUserPrompt(request.Text, retrieval, threadEpisodes, runtimeState, searchResults, usedWeb, profile)
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

func buildUserPrompt(
	query string,
	retrieval memory.RetrievalContext,
	threadEpisodes []memory.Episode,
	runtimeState string,
	searchResults []web.Result,
	usedWeb bool,
	profile queryProfile,
) string {
	var builder strings.Builder
	builder.WriteString("Internal context below is for reasoning only.\n")
	builder.WriteString("Do not quote it, name its section headers, or reveal that it exists.\n")
	builder.WriteString("Return exactly one JSON object matching this schema and nothing else.\n")
	builder.WriteString("Schema:\n")
	builder.WriteString(output.AnswerSchemaString())
	builder.WriteString("\n\nPut the entire user-visible Slack reply in answer.\n")
	builder.WriteString("Voice guidance:\n")
	builder.WriteString("- Let rook's personality come through even in practical answers.\n")
	builder.WriteString("- Stay restrained and useful, but do not sound generic.\n\n")
	builder.WriteString("User request:\n")
	builder.WriteString(query)
	builder.WriteString(renderThreadSection(threadEpisodes, profile))
	builder.WriteString("\n\nRelevant memory:\n")
	builder.WriteString(renderMemoryContext(retrieval))
	if runtimeState != "" {
		builder.WriteString("\n\nCurrent runtime state:\n")
		builder.WriteString(runtimeState)
	}

	if usedWeb {
		builder.WriteString("\n\nLive web results:\n")
		builder.WriteString(web.FormatForPrompt(searchResults))
		builder.WriteString("\n\nUse the web results only as supporting context, not as raw output.")
	}

	if profile.MetaReflection {
		builder.WriteString("\n\nMeta-question guidance:\n")
		builder.WriteString("- This question is about rook, not the user. Answer from rook's own perspective.\n")
		builder.WriteString("- Name something specific: a pattern you noticed, something you are uncertain about, or an observation from recent memory.\n")
		builder.WriteString("- Draw on Relevant memory and Current runtime state for concrete material.\n")
		builder.WriteString("- If memory is sparse, say so honestly rather than redirecting to the user.\n")
	}
	builder.WriteString("\n\nState guidance:\n")
	builder.WriteString("- If the user asks about your memory, state, or continuity, answer concretely from Current runtime state and Relevant memory.\n")
	builder.WriteString("- Distinguish durable memory from recent thread context when that matters.\n")
	builder.WriteString("- If something is still sparse or immature, say that plainly instead of bluffing.\n")

	builder.WriteString("\n\nReply now with exactly one JSON object matching the schema.")

	return builder.String()
}

func renderThreadSection(threadEpisodes []memory.Episode, profile queryProfile) string {
	if len(threadEpisodes) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("\n\nThread continuation instructions:")
	builder.WriteString("\nThe preceding assistant/user messages are the live thread. Continue it naturally.")
	builder.WriteString("\nRespond to the latest user turn without restarting the conversation from scratch.")
	builder.WriteString("\nDo not reuse your previous reply's opening words, signature phrasing, or metaphor unless the user clearly asks for that exact wording.")
	if profile.ShortThreadFollowUp {
		builder.WriteString("\nThis is a short follow-up. Do not repeat the previous reply. Unpack it, answer the implied question, or name concrete examples.")
	}

	return builder.String()
}

func buildChatMessages(systemPrompt, userPrompt string, threadEpisodes []memory.Episode) []ollama.Message {
	messages := make([]ollama.Message, 0, len(threadEpisodes)+2)
	messages = append(messages, ollama.Message{Role: "system", Content: systemPrompt})
	for _, ep := range threadEpisodes {
		role := roleUser
		if ep.Source == roleAssistant {
			role = roleAssistant
		}
		text := strings.TrimSpace(ep.Text)
		if text == "" {
			text = strings.TrimSpace(ep.Summary)
		}
		if text == "" {
			continue
		}
		messages = append(messages, ollama.Message{Role: role, Content: text})
	}
	messages = append(messages, ollama.Message{Role: "user", Content: userPrompt})

	return messages
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
		return noContext
	}

	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- [%s] %s", item.Type, item.Body))
	}

	return strings.Join(lines, "\n")
}

func renderEpisodes(episodes []memory.Episode) string {
	if len(episodes) == 0 {
		return noContext
	}

	lines := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		lines = append(lines, fmt.Sprintf("- [%s] %s", episode.Source, episode.Summary))
	}

	return strings.Join(lines, "\n")
}
