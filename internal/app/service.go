package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/JR-G/rook/internal/agent"
	"github.com/JR-G/rook/internal/commands"
	"github.com/JR-G/rook/internal/config"
	"github.com/JR-G/rook/internal/integrations/squad0"
	"github.com/JR-G/rook/internal/memory"
	"github.com/JR-G/rook/internal/ollama"
	"github.com/JR-G/rook/internal/output"
	"github.com/JR-G/rook/internal/persona"
	slacktransport "github.com/JR-G/rook/internal/slack"
	"github.com/JR-G/rook/internal/tools/web"
)

// Service wires the persistent runtime together.
type Service struct {
	cfgPath string
	logger  *slog.Logger
	started time.Time
	now     func() time.Time

	mu          sync.RWMutex
	cfg         config.Config
	lastFailure string
	lastFailed  time.Time

	store     *memory.Store
	ollama    *ollama.Client
	persona   *persona.Manager
	agent     *agent.Service
	transport *slacktransport.Transport
	observer  squad0.Observer
	sem       chan struct{}
}

// New creates a new runnable service.
func New(cfgPath string, cfg config.Config, logger *slog.Logger) (*Service, error) {
	if strings.TrimSpace(cfg.Slack.BotToken) == "" || strings.TrimSpace(cfg.Slack.AppToken) == "" {
		return nil, errors.New("slack bot and app tokens are required")
	}

	now := time.Now
	store, err := memory.Open(cfg.Memory.DBPath)
	if err != nil {
		return nil, err
	}

	personaManager := persona.New(
		store,
		cfg.Persona.CoreConstitutionFile,
		cfg.Persona.StableIdentitySeed,
		cfg.Persona.VoiceSeedFile,
		cfg.Memory.ConsolidationInterval.Duration,
		now,
	)
	if err := personaManager.Seed(context.Background()); err != nil {
		_ = store.Close()
		return nil, err
	}

	ollamaClient := ollama.New(
		cfg.Ollama.Host,
		cfg.Ollama.ChatTimeout.Duration,
		cfg.Ollama.EmbedTimeout.Duration,
	)
	searcher := buildSearcher(cfg)
	agentService := agent.New(
		ollamaClient,
		store,
		personaManager,
		searcher,
		output.New(),
		agent.Config{
			ChatModel:          cfg.Ollama.ChatModel,
			EmbeddingModel:     cfg.Ollama.EmbeddingModel,
			Temperature:        cfg.Ollama.Temperature,
			MinWriteImportance: cfg.Memory.MinWriteImportance,
			MaxPromptItems:     cfg.Memory.MaxPromptItems,
			MaxEpisodeItems:    cfg.Memory.MaxEpisodeItems,
			WebEnabled:         cfg.Web.Enabled,
			WebMaxResults:      cfg.Web.MaxResults,
			AutoOnFreshness:    cfg.Web.AutoOnFreshness,
		},
	)

	return &Service{
		cfgPath:   cfgPath,
		logger:    logger,
		started:   now().UTC(),
		now:       now,
		cfg:       cfg,
		store:     store,
		ollama:    ollamaClient,
		persona:   personaManager,
		agent:     agentService,
		transport: slacktransport.New(cfg.Slack.BotToken, cfg.Slack.AppToken, logger),
		observer: squad0.New(squad0.Config{
			Enabled:         cfg.Squad0.Enabled,
			ObservedUserIDs: cfg.Squad0.ObservedUserIDs,
			ObservedBotIDs:  cfg.Squad0.ObservedBotIDs,
			Keywords:        cfg.Squad0.Keywords,
		}),
		sem: make(chan struct{}, cfg.Slack.MaxConcurrentHandlers),
	}, nil
}

// Close shuts down local resources.
func (s *Service) Close() error {
	return s.store.Close()
}

// Run starts the reminder loop and Slack transport.
func (s *Service) Run(ctx context.Context) error {
	go s.runReminderLoop(ctx)

	err := s.transport.Run(ctx, s.HandleInbound)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.recordFailure(err)
	}

	return err
}

// HandleInbound accepts one Slack event and schedules its processing.
func (s *Service) HandleInbound(ctx context.Context, message slacktransport.InboundMessage) {
	select {
	case s.sem <- struct{}{}:
	default:
		s.logger.Warn("message handler capacity reached")
		s.recordFailure(fmt.Errorf("message handler capacity reached"))
		return
	}

	go func() {
		defer func() { <-s.sem }()
		if err := s.processMessage(ctx, message); err != nil {
			s.recordFailure(err)
			s.logger.Error("message handling failed", "error", err)
			if postErr := s.transport.PostMessage(context.Background(), message.ChannelID, message.ThreadTS, "I hit an internal error handling that message."); postErr != nil {
				s.logger.Error("failed to post error reply", "error", postErr)
			}
		}
	}()
}

func (s *Service) processMessage(ctx context.Context, message slacktransport.InboundMessage) error {
	if s.observer.Relevant(squad0.Message{
		UserID: message.UserID,
		BotID:  message.BotID,
		Text:   message.Text,
	}) {
		if _, err := s.store.RecordEpisode(ctx, memory.EpisodeInput{
			ChannelID: message.ChannelID,
			ThreadTS:  message.ThreadTS,
			UserID:    message.UserID,
			Role:      "observer",
			Source:    "squad0",
			Text:      message.Text,
		}); err != nil {
			return err
		}
	}

	if !s.shouldRespond(message) {
		return nil
	}

	text := s.normaliseText(message.Text)
	if text == "" {
		return nil
	}

	location, err := s.cfg.Location()
	if err != nil {
		return err
	}

	if reminder, ok, reminderErr := commands.ParseReminder(s.now().In(location), location, text); ok {
		if reminderErr != nil {
			return s.transport.PostMessage(ctx, message.ChannelID, message.ThreadTS, reminderErr.Error())
		}

		response, err := s.handleReminder(ctx, message, text, reminder)
		if err != nil {
			return err
		}

		return s.postLocalCommand(ctx, message, text, response)
	}

	command, ok := commands.Parse(text)
	if ok {
		response, err := s.executeCommand(ctx, message, text, command)
		if err != nil {
			return err
		}

		return s.postLocalCommand(ctx, message, text, response)
	}

	response, err := s.agent.Respond(ctx, agent.Request{
		ChannelID: message.ChannelID,
		ThreadTS:  message.ThreadTS,
		UserID:    message.UserID,
		Text:      text,
	})
	if err != nil {
		return err
	}

	if err := s.store.PruneEpisodes(ctx, s.cfg.Memory.EpisodeRetentionDays); err != nil {
		return err
	}

	return s.transport.PostMessage(ctx, message.ChannelID, message.ThreadTS, response.Text)
}

func (s *Service) shouldRespond(message slacktransport.InboundMessage) bool {
	cfg := s.currentConfig()
	if contains(cfg.Slack.DeniedChannels, message.ChannelID) {
		return false
	}
	if len(cfg.Slack.AllowedChannels) > 0 && !contains(cfg.Slack.AllowedChannels, message.ChannelID) {
		return false
	}
	if message.IsDM {
		return cfg.Slack.AllowDM
	}
	if cfg.Slack.MentionRequiredInChannels {
		return message.Mentioned
	}

	return true
}

func (s *Service) normaliseText(text string) string {
	status := s.transport.Status()
	normalised := strings.TrimSpace(text)
	if status.BotUserID != "" {
		normalised = strings.ReplaceAll(normalised, fmt.Sprintf("<@%s>", status.BotUserID), "")
	}

	return strings.TrimSpace(normalised)
}

func (s *Service) executeCommand(
	ctx context.Context,
	message slacktransport.InboundMessage,
	text string,
	command commands.Command,
) (string, error) {
	switch command.Kind {
	case commands.KindHelp:
		return helpText(), nil
	case commands.KindPing:
		return fmt.Sprintf("pong\nuptime: %s\nmodel: %s", s.now().UTC().Sub(s.started).Round(time.Second), s.agent.ChatModel()), nil
	case commands.KindStatus:
		return s.statusText(ctx)
	case commands.KindMemory:
		return s.memoryText(ctx, strings.TrimSpace(command.Args))
	case commands.KindModel:
		return s.modelText(strings.TrimSpace(command.Args)), nil
	case commands.KindReload:
		return s.reload(), nil
	case commands.KindRemind:
		return "Usage:\nremind me in 30m to stretch\nremind me at 2026-04-01 18:00 to call someone", nil
	default:
		return "", fmt.Errorf("unsupported command %q", command.Kind)
	}
}

func (s *Service) handleReminder(
	ctx context.Context,
	message slacktransport.InboundMessage,
	text string,
	reminder commands.ReminderRequest,
) (string, error) {
	scheduled, err := s.store.AddReminder(ctx, memory.ReminderInput{
		ChannelID: message.ChannelID,
		ThreadTS:  message.ThreadTS,
		Message:   reminder.Message,
		DueAt:     reminder.DueAt,
		CreatedBy: message.UserID,
	})
	if err != nil {
		return "", err
	}

	if _, err := s.store.UpsertMemory(ctx, memory.Candidate{
		Type:       memory.Commitment,
		Scope:      memory.ScopeUser,
		Subject:    reminder.Message,
		Body:       fmt.Sprintf("Reminder scheduled for %s: %s", scheduled.DueAt.Format(time.RFC3339), reminder.Message),
		Keywords:   strings.Fields(strings.ToLower(reminder.Message)),
		Confidence: 0.97,
		Importance: 0.85,
		Source:     "reminder",
	}); err != nil {
		return "", err
	}

	return fmt.Sprintf("Reminder set for %s\n%s", scheduled.DueAt.Format(time.RFC3339), scheduled.Message), nil
}

func (s *Service) postLocalCommand(ctx context.Context, message slacktransport.InboundMessage, text, reply string) error {
	if _, err := s.store.RecordEpisode(ctx, memory.EpisodeInput{
		ChannelID: message.ChannelID,
		ThreadTS:  message.ThreadTS,
		UserID:    message.UserID,
		Role:      "user",
		Source:    "user",
		Text:      text,
	}); err != nil {
		return err
	}

	if err := s.transport.PostMessage(ctx, message.ChannelID, message.ThreadTS, reply); err != nil {
		return err
	}

	_, err := s.store.RecordEpisode(ctx, memory.EpisodeInput{
		ChannelID: message.ChannelID,
		ThreadTS:  message.ThreadTS,
		UserID:    "rook",
		Role:      "assistant",
		Source:    "assistant",
		Text:      reply,
	})

	return err
}

func (s *Service) statusText(ctx context.Context) (string, error) {
	slackStatus := s.transport.Status()
	ollamaHealth, ollamaErr := s.ollama.Health(ctx)
	storeHealth, storeErr := s.store.Health(ctx)
	pendingReminders, reminderErr := s.store.PendingReminderCount(ctx)

	var builder strings.Builder
	builder.WriteString("rook status\n")
	builder.WriteString(fmt.Sprintf("uptime: %s\n", s.now().UTC().Sub(s.started).Round(time.Second)))
	builder.WriteString(fmt.Sprintf("slack connected: %t\n", slackStatus.Connected))
	builder.WriteString(fmt.Sprintf("slack last event: %s\n", formatTime(slackStatus.LastEventAt)))
	builder.WriteString(fmt.Sprintf("ollama reachable: %t\n", ollamaErr == nil && ollamaHealth.Reachable))
	builder.WriteString(fmt.Sprintf("chat model: %s\n", s.agent.ChatModel()))
	builder.WriteString(fmt.Sprintf("embedding model: %s\n", s.agent.EmbeddingModel()))
	builder.WriteString(fmt.Sprintf("memory db healthy: %t\n", storeErr == nil && storeHealth.Reachable))
	if storeErr == nil {
		builder.WriteString(fmt.Sprintf("memory items: %d\n", storeHealth.MemoryCount))
		builder.WriteString(fmt.Sprintf("episodes: %d\n", storeHealth.EpisodeCount))
	}
	if reminderErr == nil {
		builder.WriteString(fmt.Sprintf("pending reminders: %d\n", pendingReminders))
	}
	builder.WriteString(fmt.Sprintf("web enabled: %t\n", s.currentConfig().Web.Enabled))
	builder.WriteString(fmt.Sprintf("last failure: %s", s.lastFailureText()))

	return builder.String(), nil
}

func (s *Service) memoryText(ctx context.Context, query string) (string, error) {
	if query == "" {
		items, err := s.store.ListRecentMemories(ctx, 6)
		if err != nil {
			return "", err
		}
		if len(items) == 0 {
			return "No durable memory stored yet.", nil
		}

		var lines []string
		lines = append(lines, "Recent durable memory:")
		for _, item := range items {
			lines = append(lines, fmt.Sprintf("- [%s] %s", item.Type, item.Body))
		}

		return strings.Join(lines, "\n"), nil
	}

	queryEmbedding, _ := s.ollama.Embed(ctx, s.agent.EmbeddingModel(), query)
	hits, err := s.store.SearchMemories(ctx, query, queryEmbedding, 6)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "No matching memory found.", nil
	}

	var lines []string
	lines = append(lines, "Matching memory:")
	for _, hit := range hits {
		lines = append(lines, fmt.Sprintf("- [%s] %.2f %s", hit.Item.Type, hit.Score, hit.Item.Body))
	}

	return strings.Join(lines, "\n"), nil
}

func (s *Service) modelText(args string) string {
	if args == "" {
		return fmt.Sprintf(
			"chat model: %s\nembedding model: %s",
			s.agent.ChatModel(),
			s.agent.EmbeddingModel(),
		)
	}

	model := strings.TrimSpace(strings.TrimPrefix(args, "set "))
	s.agent.SetChatModel(model)

	return fmt.Sprintf("chat model set to %s", model)
}

func (s *Service) reload() string {
	cfg, err := config.Load(s.cfgPath)
	if err != nil {
		s.recordFailure(err)
		return fmt.Sprintf("reload failed: %v", err)
	}

	s.mu.Lock()
	s.cfg = cfg
	s.observer = squad0.New(squad0.Config{
		Enabled:         cfg.Squad0.Enabled,
		ObservedUserIDs: cfg.Squad0.ObservedUserIDs,
		ObservedBotIDs:  cfg.Squad0.ObservedBotIDs,
		Keywords:        cfg.Squad0.Keywords,
	})
	s.sem = make(chan struct{}, cfg.Slack.MaxConcurrentHandlers)
	s.mu.Unlock()

	s.agent.UpdateConfig(agent.Config{
		ChatModel:          cfg.Ollama.ChatModel,
		EmbeddingModel:     cfg.Ollama.EmbeddingModel,
		Temperature:        cfg.Ollama.Temperature,
		MinWriteImportance: cfg.Memory.MinWriteImportance,
		MaxPromptItems:     cfg.Memory.MaxPromptItems,
		MaxEpisodeItems:    cfg.Memory.MaxEpisodeItems,
		WebEnabled:         cfg.Web.Enabled,
		WebMaxResults:      cfg.Web.MaxResults,
		AutoOnFreshness:    cfg.Web.AutoOnFreshness,
	}, buildSearcher(cfg))

	return "configuration reloaded\nnote: Slack token changes still require a process restart"
}

func (s *Service) runReminderLoop(ctx context.Context) {
	ticker := time.NewTicker(s.currentConfig().Memory.ReminderPollInterval.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.dispatchDueReminders(ctx); err != nil {
				s.recordFailure(err)
				s.logger.Error("reminder dispatch failed", "error", err)
			}
		}
	}
}

func (s *Service) dispatchDueReminders(ctx context.Context) error {
	reminders, err := s.store.DueReminders(ctx, s.now().UTC(), 20)
	if err != nil {
		return err
	}

	for _, reminder := range reminders {
		text := fmt.Sprintf("Reminder\n%s", reminder.Message)
		if err := s.transport.PostMessage(ctx, reminder.ChannelID, reminder.ThreadTS, text); err != nil {
			return err
		}
		if err := s.store.MarkReminderSent(ctx, reminder.ID, s.now().UTC()); err != nil {
			return err
		}
		if _, err := s.store.RecordEpisode(ctx, memory.EpisodeInput{
			ChannelID: reminder.ChannelID,
			ThreadTS:  reminder.ThreadTS,
			UserID:    "rook",
			Role:      "assistant",
			Source:    "assistant",
			Text:      text,
		}); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) recordFailure(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastFailure = err.Error()
	s.lastFailed = s.now().UTC()
}

func (s *Service) lastFailureText() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastFailure == "" {
		return "none"
	}

	return fmt.Sprintf("%s at %s", s.lastFailure, formatTime(s.lastFailed))
}

func (s *Service) currentConfig() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.cfg
}

func buildSearcher(cfg config.Config) web.Searcher {
	if !cfg.Web.Enabled {
		return web.NoopSearcher{}
	}

	return web.NewDuckDuckGoSearcher(cfg.Web.Timeout.Duration, cfg.Web.UserAgent)
}

func helpText() string {
	return strings.Join([]string{
		"rook commands:",
		"- help",
		"- ping",
		"- status",
		"- memory [query]",
		"- model [set <name>]",
		"- reload",
		"- remind me in 30m to stretch",
		"- remind me at 2026-04-01 18:00 to call someone",
	}, "\n")
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}

	return false
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "never"
	}

	return value.Format(time.RFC3339)
}
