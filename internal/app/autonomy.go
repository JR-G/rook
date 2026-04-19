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
	sourceAmbientAgent      = "ambient_agent"
	sourceWeeknote          = "weeknote"
	sourceReflection        = "reflection"
	weeknoteEventLimit      = 80
	reflectionEpisodesLimit = 50
	defaultWeeknoteTime     = "10:00"
	noActivity              = "- none"
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
	if err := s.backgroundConsolidate(ctx); err != nil {
		s.logger.Error("background consolidation failed", "error", err)
	}
	if err := s.reflectIfDue(ctx); err != nil {
		s.logger.Error("reflection failed", "error", err)
	}

	return s.postWeeknoteIfDue(ctx)
}

func (s *Service) backgroundConsolidate(ctx context.Context) error {
	if s.persona == nil {
		return nil
	}

	return s.persona.ConsolidateIfDue(ctx)
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
		(strings.TrimSpace(status.BotUserID) != "" && message.UserID == status.BotUserID) {
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
		s.recordFailure(err)
		s.logger.Warn("weeknote generation failed; using fallback", "error", err, "observed_events", len(observed))
		text = fallbackWeeknoteText(observed)
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
	rollup := buildWeeknoteRollup(episodes)

	var builder strings.Builder
	builder.WriteString("Prepare a Slack weeknote that feels like a weekly recap worth reading, not a dry activity log.\n")
	builder.WriteString("Return exactly one JSON object matching this schema and nothing else.\n")
	builder.WriteString("Schema:\n")
	builder.WriteString(output.AnswerSchemaString())
	builder.WriteString("\n\nConstraints:\n")
	builder.WriteString("- Sound like rook: clear, sharp, readable, and a little characterful without turning theatrical.\n")
	builder.WriteString("- Treat the weeknote like a marquee recap for the week, not a bland summary.\n")
	builder.WriteString("- Open with a strong lead, then give the channel 2-3 short sections or clusters.\n")
	builder.WriteString("- Use 2-4 well-placed emojis for tone and scanability.\n")
	builder.WriteString("- Synthesize repeated updates into one sharper point instead of repeating near-duplicate bullets.\n")
	builder.WriteString("- If one ticket or thread kept resurfacing, say that explicitly.\n")
	builder.WriteString("- Keep it compact enough for a shared channel: aim for roughly 6-10 lines total.\n")
	builder.WriteString("- Use only the observed activity below; do not invent work.\n")
	builder.WriteString("- If activity was quiet, say so plainly.\n")
	builder.WriteString("- Do not mention logs, prompts, JSON, or internal machinery.\n")
	builder.WriteString(fmt.Sprintf("\nWeek window: %s to %s\n", weekStart.Format(time.RFC3339), now.Format(time.RFC3339)))
	builder.WriteString(fmt.Sprintf("Scheduled post time: %s\n", scheduledAt.Format(time.RFC3339)))
	builder.WriteString("\nDerived cues:\n")
	builder.WriteString(formatWeeknoteRollup(rollup))
	builder.WriteString("\nObserved agent activity:\n")
	builder.WriteString(formatWeeknoteEpisodes(episodes))
	builder.WriteString("\n\nWrite the final weeknote now.")

	return builder.String()
}

func formatWeeknoteEpisodes(episodes []memory.Episode) string {
	if len(episodes) == 0 {
		return noActivity
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

func (s *Service) reflectIfDue(ctx context.Context) error {
	cfg := s.currentConfig()
	if !cfg.Autonomy.Enabled || !cfg.Autonomy.ReflectionEnabled || s.persona == nil {
		return nil
	}

	interval := cfg.Autonomy.ReflectionInterval.Duration
	if interval <= 0 {
		interval = 24 * time.Hour
	}

	recentEpisodes, err := s.store.RecentEpisodes(ctx, reflectionEpisodesLimit)
	if err != nil {
		return err
	}

	cutoff := s.now().UTC().Add(-interval)
	if hasReflectionSince(recentEpisodes, cutoff) {
		return nil
	}

	sinceLast := episodesSince(recentEpisodes, cutoff)
	if len(sinceLast) == 0 {
		return nil
	}

	text, err := s.composeReflection(ctx, cfg, sinceLast)
	if err != nil {
		return err
	}

	s.logger.Info("recording self-reflection", "episode_count", len(sinceLast))

	if _, err := s.store.RecordEpisode(ctx, memory.EpisodeInput{
		ChannelID: "",
		ThreadTS:  "",
		UserID:    "rook",
		Role:      "assistant",
		Source:    sourceReflection,
		Text:      text,
	}); err != nil {
		return err
	}

	if channel := strings.TrimSpace(cfg.Autonomy.ReflectionChannel); channel != "" {
		s.postReflection(ctx, channel, text)
	}

	return nil
}

func (s *Service) postReflection(ctx context.Context, channel, text string) {
	if err := s.transport.PostMessage(ctx, channel, "", text); err != nil {
		s.logger.Error("failed to post reflection", "channel_id", channel, "error", err)
	}
}

func (s *Service) composeReflection(
	ctx context.Context,
	cfg config.Config,
	episodes []memory.Episode,
) (string, error) {
	systemPrompt, err := s.persona.RenderSystemPrompt(ctx)
	if err != nil {
		return "", err
	}

	prompt := buildReflectionPrompt(episodes, s.now().UTC())
	result, err := s.chatAutonomyWithFallback(ctx, cfg, []ollama.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return "", err
	}

	return output.ParseAnswer(result.Content)
}

func buildReflectionPrompt(episodes []memory.Episode, now time.Time) string {
	var builder strings.Builder
	builder.WriteString("You are reflecting privately on your recent activity. This is not a conversation or a status report. It should read like a real internal note.\n")
	builder.WriteString("Return exactly one JSON object matching this schema and nothing else.\n")
	builder.WriteString("Schema:\n")
	builder.WriteString(output.AnswerSchemaString())
	builder.WriteString("\n\nReview the activity below and write a brief, honest reflection.\n")
	builder.WriteString("Consider:\n")
	builder.WriteString("- What concrete thread, phrase, ticket, or decision kept resurfacing?\n")
	builder.WriteString("- Where did the signal sharpen, and where did it get diluted?\n")
	builder.WriteString("- What should you watch more closely next time?\n")
	builder.WriteString("- Are there gaps in what you know about the user or their work?\n")
	builder.WriteString("- How is your conversational quality? Are you repeating yourself, flattening your voice, or actually being useful?\n")
	builder.WriteString("\nConstraints:\n")
	builder.WriteString("- Keep it to 2-4 sentences.\n")
	builder.WriteString("- Sound like rook thinking in real time, not writing a sanitised retro.\n")
	builder.WriteString("- Make at least one concrete reference to the observed activity when there is one.\n")
	builder.WriteString("- Prefer one sharp judgement and one course correction over a generic recap.\n")
	builder.WriteString("- Avoid template phrasing like \"The pattern is\", \"I notice\", \"I should pay closer attention\", or \"Overall\".\n")
	builder.WriteString("- If nothing stands out, say so plainly — do not invent observations.\n")
	builder.WriteString("- Do not mention JSON, schemas, internal prompts, or system mechanics.\n")
	builder.WriteString(fmt.Sprintf("\nCurrent time: %s\n", now.Format(time.RFC3339)))
	builder.WriteString("\nRecent cues:\n")
	builder.WriteString(formatReflectionCues(episodes))
	builder.WriteString("\nRecent activity:\n")
	builder.WriteString(formatReflectionEpisodes(episodes))
	builder.WriteString("\n\nWrite the reflection now.")

	return builder.String()
}

func formatReflectionEpisodes(episodes []memory.Episode) string {
	if len(episodes) == 0 {
		return noActivity
	}

	lines := make([]string, 0, len(episodes))
	for _, ep := range episodes {
		text := strings.TrimSpace(ep.Summary)
		if text == "" {
			text = strings.TrimSpace(ep.Text)
		}
		if text == "" {
			continue
		}
		if len(text) > 200 {
			text = text[:200] + "…"
		}
		lines = append(lines, fmt.Sprintf("- [%s] %s: %s", ep.CreatedAt.Format(time.RFC3339), ep.Source, text))
	}
	if len(lines) == 0 {
		return noActivity
	}

	return strings.Join(lines, "\n")
}

func hasReflectionSince(episodes []memory.Episode, since time.Time) bool {
	for _, ep := range episodes {
		if ep.Source == sourceReflection && !ep.CreatedAt.Before(since) {
			return true
		}
	}

	return false
}

func episodesSince(episodes []memory.Episode, since time.Time) []memory.Episode {
	filtered := make([]memory.Episode, 0, len(episodes))
	for _, ep := range episodes {
		if !ep.CreatedAt.Before(since) {
			filtered = append(filtered, ep)
		}
	}

	return filtered
}
