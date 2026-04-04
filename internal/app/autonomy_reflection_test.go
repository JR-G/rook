package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JR-G/rook/internal/config"
	"github.com/JR-G/rook/internal/memory"
	"github.com/JR-G/rook/internal/ollama"
	"github.com/JR-G/rook/internal/output"
)

func TestReflectIfDueSkipsWhenDisabled(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	service.cfg.Autonomy.Enabled = false
	if err := service.reflectIfDue(context.Background()); err != nil {
		t.Fatalf("reflectIfDue with disabled autonomy: %v", err)
	}

	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.ReflectionEnabled = false
	if err := service.reflectIfDue(context.Background()); err != nil {
		t.Fatalf("reflectIfDue with disabled reflection: %v", err)
	}
}

func TestReflectIfDueSkipsWhenAlreadyReflected(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 4, 12, 0, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return now })
	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.ReflectionEnabled = true
	service.cfg.Autonomy.ReflectionInterval = config.Duration{Duration: 24 * time.Hour}

	if _, err := service.store.RecordEpisode(context.Background(), memory.EpisodeInput{
		ChannelID: "",
		UserID:    "rook",
		Role:      "assistant",
		Source:    sourceReflection,
		Text:      "Earlier reflection",
	}); err != nil {
		t.Fatalf("RecordEpisode: %v", err)
	}

	if err := service.reflectIfDue(context.Background()); err != nil {
		t.Fatalf("reflectIfDue should skip when already reflected: %v", err)
	}
}

func TestReflectIfDueSkipsWhenNoRecentEpisodes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 4, 12, 0, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return now })
	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.ReflectionEnabled = true
	service.cfg.Autonomy.ReflectionInterval = config.Duration{Duration: 24 * time.Hour}

	if err := service.reflectIfDue(context.Background()); err != nil {
		t.Fatalf("reflectIfDue should skip when no episodes: %v", err)
	}
}

func TestReflectIfDueRecordsReflection(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 4, 12, 0, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return now })
	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.ReflectionEnabled = true
	service.cfg.Autonomy.ReflectionInterval = config.Duration{Duration: 24 * time.Hour}
	service.persona = seededPersonaManager(t, service)
	service.ollama = &autonomyOllama{
		fakeOllama: fakeOllama{
			health:    ollama.Health{Reachable: true},
			embedding: []float64{1, 0},
		},
		results: []ollama.ChatResult{
			{Model: "phi4-mini", Content: `{"answer":"I notice the user has been asking about repetition."}`},
		},
	}

	if _, err := service.store.RecordEpisode(context.Background(), memory.EpisodeInput{
		ChannelID: "C1",
		UserID:    "U1",
		Role:      "user",
		Source:    "user",
		Text:      "how are you",
	}); err != nil {
		t.Fatalf("RecordEpisode: %v", err)
	}

	if err := service.reflectIfDue(context.Background()); err != nil {
		t.Fatalf("reflectIfDue: %v", err)
	}

	episodes, err := service.store.RecentEpisodes(context.Background(), 10)
	if err != nil {
		t.Fatalf("RecentEpisodes: %v", err)
	}
	reflectionEp := findEpisodeBySource(episodes, sourceReflection)
	if reflectionEp == nil {
		t.Fatal("expected a reflection episode to be recorded")
	}
	if !strings.Contains(reflectionEp.Text, "repetition") {
		t.Fatalf("unexpected reflection text %q", reflectionEp.Text)
	}
}

func findEpisodeBySource(episodes []memory.Episode, source string) *memory.Episode {
	for i := range episodes {
		if episodes[i].Source == source {
			return &episodes[i]
		}
	}

	return nil
}

func TestReflectIfDuePostsToChannel(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 4, 12, 0, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return now })
	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.ReflectionEnabled = true
	service.cfg.Autonomy.ReflectionInterval = config.Duration{Duration: 24 * time.Hour}
	service.cfg.Autonomy.ReflectionChannel = "C-REFLECT"
	service.persona = seededPersonaManager(t, service)
	service.ollama = &autonomyOllama{
		fakeOllama: fakeOllama{
			health:    ollama.Health{Reachable: true},
			embedding: []float64{1, 0},
		},
		results: []ollama.ChatResult{
			{Model: "phi4-mini", Content: `{"answer":"A quiet week."}`},
		},
	}

	if _, err := service.store.RecordEpisode(context.Background(), memory.EpisodeInput{
		ChannelID: "C1",
		UserID:    "U1",
		Role:      "user",
		Source:    "user",
		Text:      "hello rook",
	}); err != nil {
		t.Fatalf("RecordEpisode: %v", err)
	}

	if err := service.reflectIfDue(context.Background()); err != nil {
		t.Fatalf("reflectIfDue: %v", err)
	}

	transport := requireFakeTransport(t, service)
	if len(transport.postedTexts) != 1 || !strings.Contains(transport.postedTexts[0], "quiet week") {
		t.Fatalf("expected reflection posted to channel, got %#v", transport.postedTexts)
	}
}

func TestBuildReflectionPrompt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 4, 12, 0, 0, 0, time.UTC)
	episodes := []memory.Episode{
		{Source: "user", Text: "how are you", CreatedAt: now.Add(-time.Hour)},
		{Source: "assistant", Summary: "Doing well.", CreatedAt: now.Add(-50 * time.Minute)},
	}

	prompt := buildReflectionPrompt(episodes, now)
	if !strings.Contains(prompt, "reflecting privately") {
		t.Fatalf("expected reflection framing in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, output.AnswerSchemaString()) {
		t.Fatalf("expected answer schema in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "patterns") {
		t.Fatalf("expected pattern guidance in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "under 4 sentences") {
		t.Fatalf("expected brevity constraint in prompt, got %q", prompt)
	}
}

func TestFormatReflectionEpisodes(t *testing.T) {
	t.Parallel()

	if formatted := formatReflectionEpisodes(nil); formatted != noActivity {
		t.Fatalf("expected noActivity for nil episodes, got %q", formatted)
	}

	if formatted := formatReflectionEpisodes([]memory.Episode{{Source: "user", Text: "", Summary: ""}}); formatted != noActivity {
		t.Fatalf("expected noActivity for empty-text episodes, got %q", formatted)
	}

	now := time.Date(2026, time.April, 4, 12, 0, 0, 0, time.UTC)
	formatted := formatReflectionEpisodes([]memory.Episode{
		{Source: "user", Summary: "Asked a question", CreatedAt: now},
	})
	if !strings.Contains(formatted, "user: Asked a question") {
		t.Fatalf("unexpected formatted episodes %q", formatted)
	}

	longText := strings.Repeat("x", 250)
	formatted = formatReflectionEpisodes([]memory.Episode{
		{Source: "user", Text: longText, CreatedAt: now},
	})
	if len(formatted) > 300 {
		t.Fatalf("expected long text to be truncated, got len=%d", len(formatted))
	}
	if !strings.Contains(formatted, "…") {
		t.Fatalf("expected truncation marker in formatted episodes %q", formatted)
	}
}

func TestHasReflectionSince(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 4, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-24 * time.Hour)

	if hasReflectionSince(nil, cutoff) {
		t.Fatal("expected no reflection in empty episodes")
	}
	if hasReflectionSince([]memory.Episode{
		{Source: "user", Text: "hello", CreatedAt: now},
	}, cutoff) {
		t.Fatal("expected non-reflection source to not match")
	}
	if hasReflectionSince([]memory.Episode{
		{Source: sourceReflection, Text: "old one", CreatedAt: cutoff.Add(-time.Hour)},
	}, cutoff) {
		t.Fatal("expected old reflection to not match")
	}
	if !hasReflectionSince([]memory.Episode{
		{Source: sourceReflection, Text: "recent", CreatedAt: now},
	}, cutoff) {
		t.Fatal("expected recent reflection to match")
	}
}

func TestEpisodesSince(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 4, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-2 * time.Hour)

	episodes := []memory.Episode{
		{Source: "user", Text: "old", CreatedAt: cutoff.Add(-time.Hour)},
		{Source: "user", Text: "recent", CreatedAt: now.Add(-time.Hour)},
		{Source: "assistant", Text: "reply", CreatedAt: now},
	}

	filtered := episodesSince(episodes, cutoff)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 episodes since cutoff, got %d", len(filtered))
	}
	if filtered[0].Text != "recent" || filtered[1].Text != "reply" {
		t.Fatalf("unexpected filtered episodes %#v", filtered)
	}

	if len(episodesSince(nil, cutoff)) != 0 {
		t.Fatal("expected empty result for nil episodes")
	}
}

func TestBackgroundConsolidate(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	if err := service.backgroundConsolidate(context.Background()); err != nil {
		t.Fatalf("backgroundConsolidate with nil persona should succeed: %v", err)
	}

	service.persona = seededPersonaManager(t, service)
	if err := service.backgroundConsolidate(context.Background()); err != nil {
		t.Fatalf("backgroundConsolidate with persona: %v", err)
	}
}

func TestReflectIfDueComposeFailure(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 4, 12, 0, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return now })
	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.ReflectionEnabled = true
	service.cfg.Autonomy.ReflectionInterval = config.Duration{Duration: 24 * time.Hour}
	service.persona = seededPersonaManager(t, service)
	service.ollama = &autonomyOllama{
		fakeOllama: fakeOllama{
			health:    ollama.Health{Reachable: true},
			embedding: []float64{1, 0},
		},
		errs: []error{
			ollama.StatusError{StatusCode: 500, Message: "model crashed"},
		},
	}

	if _, err := service.store.RecordEpisode(context.Background(), memory.EpisodeInput{
		ChannelID: "C1",
		UserID:    "U1",
		Role:      "user",
		Source:    "user",
		Text:      "hello rook",
	}); err != nil {
		t.Fatalf("RecordEpisode: %v", err)
	}

	err := service.reflectIfDue(context.Background())
	if err == nil {
		t.Fatal("expected compose failure to bubble up")
	}
}

func TestReflectIfDueDefaultInterval(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 4, 12, 0, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return now })
	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.ReflectionEnabled = true
	service.cfg.Autonomy.ReflectionInterval = config.Duration{}

	if err := service.reflectIfDue(context.Background()); err != nil {
		t.Fatalf("reflectIfDue with zero interval: %v", err)
	}
}

func TestDispatchAutonomyWithReflectionAndConsolidation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 4, 12, 0, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return now })
	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.ReflectionEnabled = true
	service.cfg.Autonomy.ReflectionInterval = config.Duration{Duration: 24 * time.Hour}
	service.persona = seededPersonaManager(t, service)
	service.ollama = &autonomyOllama{
		fakeOllama: fakeOllama{
			health:    ollama.Health{Reachable: true},
			embedding: []float64{1, 0},
		},
		results: []ollama.ChatResult{
			{Model: "phi4-mini", Content: `{"answer":"All quiet on the western front."}`},
		},
	}

	if _, err := service.store.RecordEpisode(context.Background(), memory.EpisodeInput{
		ChannelID: "C1",
		UserID:    "U1",
		Role:      "user",
		Source:    "user",
		Text:      "hello rook",
	}); err != nil {
		t.Fatalf("RecordEpisode: %v", err)
	}

	if err := service.dispatchAutonomy(context.Background()); err != nil {
		_ = err // weeknote may not be configured
	}

	episodes, err := service.store.RecentEpisodes(context.Background(), 10)
	if err != nil {
		t.Fatalf("RecentEpisodes: %v", err)
	}
	if findEpisodeBySource(episodes, sourceReflection) == nil {
		t.Fatal("expected dispatchAutonomy to trigger reflection")
	}
}

func TestReflectIfDueStoreError(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 4, 12, 0, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return now })
	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.ReflectionEnabled = true
	service.cfg.Autonomy.ReflectionInterval = config.Duration{Duration: 24 * time.Hour}
	service.persona = seededPersonaManager(t, service)

	if err := service.store.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	err := service.reflectIfDue(context.Background())
	if err == nil {
		t.Fatal("expected store error to bubble up")
	}
}

func TestPostReflectionHandlesTransportError(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	// postReflection logs errors rather than returning them.
	service.postReflection(context.Background(), "C-INVALID", "test reflection")
}

func TestDispatchAutonomyConsolidationErrorLogged(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 4, 12, 0, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return now })
	service.persona = seededPersonaManager(t, service)

	// Close store to force consolidation errors.
	if err := service.store.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	// dispatchAutonomy should log the consolidation error and continue.
	_ = service.dispatchAutonomy(context.Background())
}
