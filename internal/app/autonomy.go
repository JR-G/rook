package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/JR-G/rook/internal/config"
	"github.com/JR-G/rook/internal/memory"
	"github.com/JR-G/rook/internal/ollama"
	"github.com/JR-G/rook/internal/output"
	slacktransport "github.com/JR-G/rook/internal/slack"
)

const (
	sourceAmbientAgent  = "ambient_agent"
	sourceWeeknote      = "weeknote"
	weeknoteEventLimit  = 80
	defaultWeeknoteTime = "10:00"
)

func (s *Service) runAutonomyLoop(ctx context.Context) {
	pollInterval := s.currentConfig().Autonomy.PollInterval.Duration
	if pollInterval <= 0 {
		pollInterval = time.Minute
	}

	if err := s.dispatchAutonomy(ctx); err != nil {
		s.recordFailure(err)
		s.logger.Error("autonomy tick failed", "error", err)
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.dispatchAutonomy(ctx); err != nil {
				s.recordFailure(err)
				s.logger.Error("autonomy tick failed", "error", err)
			}
		}
	}
}

func (s *Service) dispatchAutonomy(ctx context.Context) error {
	return s.postWeeknoteIfDue(ctx)
}

func (s *Service) observeAmbientActivity(
	ctx context.Context,
	message slacktransport.InboundMessage,
	shouldRespond bool,
) (bool, error) {
	observed, err := s.observeSquad0(ctx, message)
	if err != nil || observed {
		return observed, err
	}
	if shouldRespond {
		return false, nil
	}

	cfg := s.currentConfig()
	status := s.transport.Status()
	if !cfg.Autonomy.Enabled ||
		!cfg.Autonomy.ObserveAgentChannels ||
		message.IsDM ||
		strings.TrimSpace(message.BotID) == "" ||
		message.BotID == status.BotID ||
		message.UserID == status.BotUserID {
		return false, nil
	}

	return s.recordObservedEpisode(ctx, message, sourceAmbientAgent)
}

func (s *Service) recordObservedEpisode(
	ctx context.Context,
	message slacktransport.InboundMessage,
	source string,
) (bool, error) {
	actorID := strings.TrimSpace(message.UserID)
	if actorID == "" {
		actorID = strings.TrimSpace(message.BotID)
	}

	_, err := s.store.RecordEpisode(ctx, memory.EpisodeInput{
		ChannelID: message.ChannelID,
		ThreadTS:  message.ThreadTS,
		UserID:    actorID,
		Role:      "observer",
		Source:    source,
		Text:      message.Text,
	})
	if err != nil {
		return false, err
	}

	return true, nil
}

func (s *Service) postWeeknoteIfDue(ctx context.Context) error {
	cfg := s.currentConfig()
	if !cfg.Autonomy.Enabled || !cfg.Autonomy.WeeknotesEnabled || strings.TrimSpace(cfg.Autonomy.WeeknotesChannel) == "" {
		return nil
	}

	location, err := cfg.Location()
	if err != nil {
		return err
	}
	nowLocal := s.now().In(location)
	weekStart, scheduledAt, due, err := weeknoteWindow(nowLocal, cfg.Autonomy.WeeknotePostTime)
	if err != nil || !due {
		return err
	}

	recentEpisodes, err := s.store.RecentEpisodes(ctx, 500)
	if err != nil {
		return err
	}
	if hasWeeknotePost(recentEpisodes, cfg.Autonomy.WeeknotesChannel, weekStart.UTC()) {
		return nil
	}

	observed := observedAgentEpisodes(recentEpisodes, weekStart.UTC(), nowLocal.UTC())
	text, err := s.composeWeeknote(ctx, cfg, observed, weekStart, scheduledAt, nowLocal)
	if err != nil {
		return err
	}
	s.logger.Info(
		"posting scheduled weeknote",
		"channel_id",
		cfg.Autonomy.WeeknotesChannel,
		"observed_events",
		len(observed),
	)
	if err := s.transport.PostMessage(ctx, cfg.Autonomy.WeeknotesChannel, "", text); err != nil {
		return err
	}
	_, err = s.store.RecordEpisode(ctx, memory.EpisodeInput{
		ChannelID: cfg.Autonomy.WeeknotesChannel,
		ThreadTS:  "",
		UserID:    "rook",
		Role:      "assistant",
		Source:    sourceWeeknote,
		Text:      text,
	})

	return err
}

func (s *Service) composeWeeknote(
	ctx context.Context,
	cfg config.Config,
	episodes []memory.Episode,
	weekStart time.Time,
	scheduledAt time.Time,
	now time.Time,
) (string, error) {
	systemPrompt, err := s.persona.RenderSystemPrompt(ctx)
	if err != nil {
		return "", err
	}

	prompt := buildWeeknotePrompt(episodes, weekStart, scheduledAt, now)
	result, err := s.chatAutonomyWithFallback(ctx, cfg, []ollama.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return "", err
	}

	return output.ParseAnswer(result.Content)
}

func (s *Service) chatAutonomyWithFallback(
	ctx context.Context,
	cfg config.Config,
	messages []ollama.Message,
) (ollama.ChatResult, error) {
	models := make([]string, 0, len(cfg.Ollama.ChatFallbacks)+1)
	models = append(models, strings.TrimSpace(cfg.Ollama.ChatModel))
	models = append(models, cfg.Ollama.ChatFallbacks...)

	for _, model := range models {
		trimmed := strings.TrimSpace(model)
		if trimmed == "" {
			continue
		}

		result, err := s.ollama.ChatStructured(ctx, trimmed, messages, cfg.Ollama.Temperature, output.AnswerSchema())
		if err == nil {
			return result, nil
		}
		if !ollama.ShouldFallbackModel(err) {
			return ollama.ChatResult{}, err
		}
	}

	return ollama.ChatResult{}, fmt.Errorf("no chat model configured")
}

func buildWeeknotePrompt(
	episodes []memory.Episode,
	weekStart time.Time,
	scheduledAt time.Time,
	now time.Time,
) string {
	var builder strings.Builder
	builder.WriteString("Prepare a concise Slack weeknote about what the other agents have been doing this week.\n")
	builder.WriteString("Return exactly one JSON object matching this schema and nothing else.\n")
	builder.WriteString("Schema:\n")
	builder.WriteString(output.AnswerSchemaString())
	builder.WriteString("\n\nConstraints:\n")
	builder.WriteString("- Sound like rook: clear, observant, understated, and a little sharp.\n")
	builder.WriteString("- Keep it short and readable for a shared channel.\n")
	builder.WriteString("- Use only the observed activity below; do not invent work.\n")
	builder.WriteString("- If activity was quiet, say so plainly.\n")
	builder.WriteString("- Do not mention logs, prompts, JSON, or internal machinery.\n")
	builder.WriteString(fmt.Sprintf("\nWeek window: %s to %s\n", weekStart.Format(time.RFC3339), now.Format(time.RFC3339)))
	builder.WriteString(fmt.Sprintf("Scheduled post time: %s\n", scheduledAt.Format(time.RFC3339)))
	builder.WriteString("\nObserved agent activity:\n")
	builder.WriteString(formatWeeknoteEpisodes(episodes))
	builder.WriteString("\n\nWrite the final weeknote now.")

	return builder.String()
}

func formatWeeknoteEpisodes(episodes []memory.Episode) string {
	if len(episodes) == 0 {
		return "- none"
	}

	lines := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		lines = append(lines, fmt.Sprintf(
			"- [%s] actor=%s channel=%s summary=%s",
			episode.CreatedAt.Format(time.RFC3339),
			episode.UserID,
			episode.ChannelID,
			episode.Summary,
		))
	}

	return strings.Join(lines, "\n")
}

func weeknoteWindow(now time.Time, clockHHMM string) (weekStart, scheduledAt time.Time, due bool, err error) {
	clockText := strings.TrimSpace(clockHHMM)
	if clockText == "" {
		clockText = defaultWeeknoteTime
	}
	hour, minute, parseErr := config.ParseClockHHMM(clockText)
	if parseErr != nil {
		err = fmt.Errorf("invalid weeknote clock %q", clockText)
		return
	}

	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	daysSinceMonday := (int(now.Weekday()) + 6) % 7
	weekStart = midnight.AddDate(0, 0, -daysSinceMonday)
	scheduledAt = weekStart.AddDate(0, 0, 4).Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
	due = !now.Before(scheduledAt)

	return
}

func observedAgentEpisodes(episodes []memory.Episode, since, until time.Time) []memory.Episode {
	filtered := make([]memory.Episode, 0, len(episodes))
	for _, episode := range episodes {
		if episode.CreatedAt.Before(since) || episode.CreatedAt.After(until) {
			continue
		}
		if episode.Source != "squad0" && episode.Source != sourceAmbientAgent {
			continue
		}
		filtered = append(filtered, episode)
		if len(filtered) == weeknoteEventLimit {
			break
		}
	}

	for left, right := 0, len(filtered)-1; left < right; left, right = left+1, right-1 {
		filtered[left], filtered[right] = filtered[right], filtered[left]
	}

	return filtered
}

func hasWeeknotePost(episodes []memory.Episode, channelID string, since time.Time) bool {
	for _, episode := range episodes {
		if episode.ChannelID == channelID && episode.Source == sourceWeeknote && !episode.CreatedAt.Before(since) {
			return true
		}
	}

	return false
}
