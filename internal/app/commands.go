package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/JR-G/rook/internal/agent"
	"github.com/JR-G/rook/internal/commands"
	"github.com/JR-G/rook/internal/config"
	"github.com/JR-G/rook/internal/integrations/squad0"
	"github.com/JR-G/rook/internal/memory"
	slacktransport "github.com/JR-G/rook/internal/slack"
)

const noFallbackModels = "none"

func (s *Service) executeCommand(ctx context.Context, command commands.Command) (string, error) {
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
	builder.WriteString(fmt.Sprintf("chat fallbacks: %s\n", formatFallbackModels(s.currentConfig().Ollama.ChatFallbacks)))
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
		return s.recentMemoryText(ctx)
	}

	return s.searchMemoryText(ctx, query)
}

func (s *Service) recentMemoryText(ctx context.Context) (string, error) {
	items, err := s.store.ListRecentMemories(ctx, 6)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "No durable memory stored yet.", nil
	}

	lines := make([]string, 0, len(items)+1)
	lines = append(lines, "Recent durable memory:")
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- [%s] %s", item.Type, item.Body))
	}

	return strings.Join(lines, "\n"), nil
}

func (s *Service) searchMemoryText(ctx context.Context, query string) (string, error) {
	queryEmbedding, _ := s.ollama.Embed(ctx, s.agent.EmbeddingModel(), query)
	hits, err := s.store.SearchMemories(ctx, query, queryEmbedding, 6)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "No matching memory found.", nil
	}

	lines := make([]string, 0, len(hits)+1)
	lines = append(lines, "Matching memory:")
	for _, hit := range hits {
		lines = append(lines, fmt.Sprintf("- [%s] %.2f %s", hit.Item.Type, hit.Score, hit.Item.Body))
	}

	return strings.Join(lines, "\n"), nil
}

func (s *Service) modelText(args string) string {
	if args == "" {
		return fmt.Sprintf(
			"chat model: %s\nchat fallbacks: %s\nembedding model: %s",
			s.agent.ChatModel(),
			formatFallbackModels(s.currentConfig().Ollama.ChatFallbacks),
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
		ChatFallbacks:      cfg.Ollama.ChatFallbacks,
		EmbeddingModel:     cfg.Ollama.EmbeddingModel,
		Temperature:        cfg.Ollama.Temperature,
		MinWriteImportance: cfg.Memory.MinWriteImportance,
		MaxPromptItems:     cfg.Memory.MaxPromptItems,
		MaxEpisodeItems:    cfg.Memory.MaxEpisodeItems,
		WebEnabled:         cfg.Web.Enabled,
		WebMaxResults:      cfg.Web.MaxResults,
		AutoOnFreshness:    cfg.Web.AutoOnFreshness,
	}, buildSearcher(cfg))
	if err := s.refreshPersonaOnReload(); err != nil {
		s.recordFailure(err)
		return fmt.Sprintf("reload failed: %v", err)
	}

	return "configuration reloaded\npersona refreshed from seed files\nnote: Slack token changes still require a process restart"
}

func (s *Service) refreshPersonaOnReload() error {
	if s.persona == nil {
		return nil
	}

	return s.persona.Consolidate(context.Background())
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

func formatFallbackModels(models []string) string {
	if len(models) == 0 {
		return noFallbackModels
	}

	return strings.Join(models, ", ")
}
