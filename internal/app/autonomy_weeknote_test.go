package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JR-G/rook/internal/config"
	"github.com/JR-G/rook/internal/memory"
	"github.com/JR-G/rook/internal/ollama"
	"github.com/JR-G/rook/internal/persona"
)

const (
	weeknoteChannelID = "C-WEEK"
	invalidClockValue = "bad"
	validWeeknoteTime = "10:00"
)

func TestPostWeeknoteIfDuePostsAndDedupes(t *testing.T) {
	t.Parallel()

	currentTime := time.Date(2026, time.April, 3, 10, 5, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return currentTime })
	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.WeeknotesEnabled = true
	service.cfg.Autonomy.WeeknotesChannel = weeknoteChannelID
	service.cfg.Autonomy.WeeknotePostTime = validWeeknoteTime
	service.persona = seededPersonaManager(t, service)
	service.ollama = &autonomyOllama{
		fakeOllama: fakeOllama{
			health:    ollama.Health{Reachable: true},
			embedding: []float64{1, 0},
		},
		results: []ollama.ChatResult{
			{Model: "qwen3:4b", Content: `{"answer":"Weeknote text"}`},
		},
	}

	for _, episode := range []memory.EpisodeInput{
		{ChannelID: "C1", ThreadTS: "1.0", UserID: "B1", Role: "observer", Source: sourceAmbientAgent, Text: "Agent alpha fixed the deploy flow"},
		{ChannelID: "C2", ThreadTS: "2.0", UserID: "U-SQUAD", Role: "observer", Source: "squad0", Text: "squad0 shipped the planner refactor"},
	} {
		if _, err := service.store.RecordEpisode(context.Background(), episode); err != nil {
			t.Fatalf("RecordEpisode: %v", err)
		}
	}

	if err := service.postWeeknoteIfDue(context.Background()); err != nil {
		t.Fatalf("postWeeknoteIfDue: %v", err)
	}

	transport := requireFakeTransport(t, service)
	if len(transport.postedTexts) != 1 || transport.postedTexts[0] != "Weeknote text" {
		t.Fatalf("unexpected weeknote posts %#v", transport.postedTexts)
	}

	episodes, err := service.store.RecentEpisodes(context.Background(), 10)
	if err != nil {
		t.Fatalf("RecentEpisodes: %v", err)
	}
	if !hasWeeknotePost(episodes, weeknoteChannelID, currentTime.Add(-24*time.Hour)) {
		t.Fatalf("expected stored weeknote episode %#v", episodes)
	}

	if err := service.postWeeknoteIfDue(context.Background()); err != nil {
		t.Fatalf("postWeeknoteIfDue second run: %v", err)
	}
	if len(transport.postedTexts) != 1 {
		t.Fatalf("expected deduped weeknote posts %#v", transport.postedTexts)
	}
}

func TestPostWeeknoteIfDueHandlesDisabledAndErrors(t *testing.T) {
	t.Parallel()

	currentTime := time.Date(2026, time.April, 3, 10, 5, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return currentTime })
	service.cfg.Autonomy.Enabled = false
	if err := service.postWeeknoteIfDue(context.Background()); err != nil {
		t.Fatalf("disabled postWeeknoteIfDue: %v", err)
	}

	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.WeeknotesEnabled = true
	service.cfg.Autonomy.WeeknotesChannel = weeknoteChannelID
	service.cfg.Autonomy.WeeknotePostTime = validWeeknoteTime
	service.persona = seededPersonaManager(t, service)
	service.ollama = &autonomyOllama{
		fakeOllama: fakeOllama{
			health:    ollama.Health{Reachable: true},
			embedding: []float64{1, 0},
		},
		results: []ollama.ChatResult{
			{Model: "qwen3:4b", Content: `{"answer":"Weeknote text"}`},
		},
	}
	service.transport = &errorTransport{postErr: errors.New("post failed")}
	if err := service.postWeeknoteIfDue(context.Background()); err == nil || !strings.Contains(err.Error(), "post failed") {
		t.Fatalf("expected post failure, got %v", err)
	}

	service.cfg.Autonomy.WeeknotePostTime = invalidClockValue
	if err := service.postWeeknoteIfDue(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid weeknote clock") {
		t.Fatalf("expected invalid clock error, got %v", err)
	}
}

func TestPostWeeknoteIfDueFallsBackOnMalformedOutput(t *testing.T) {
	t.Parallel()

	currentTime := time.Date(2026, time.April, 10, 10, 5, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return currentTime })
	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.WeeknotesEnabled = true
	service.cfg.Autonomy.WeeknotesChannel = weeknoteChannelID
	service.cfg.Autonomy.WeeknotePostTime = validWeeknoteTime
	service.persona = seededPersonaManager(t, service)
	service.ollama = &autonomyOllama{
		fakeOllama: fakeOllama{
			health:    ollama.Health{Reachable: true},
			embedding: []float64{1, 0},
		},
		results: []ollama.ChatResult{
			{Model: "qwen3:4b", Content: "not-json"},
		},
	}

	for _, episode := range []memory.EpisodeInput{
		{ChannelID: "C1", ThreadTS: "1.0", UserID: "B1", Role: "observer", Source: sourceAmbientAgent, Text: "Agent alpha fixed the deploy flow"},
		{ChannelID: "C2", ThreadTS: "2.0", UserID: "U-SQUAD", Role: "observer", Source: "squad0", Text: "squad0 shipped the planner refactor"},
	} {
		if _, err := service.store.RecordEpisode(context.Background(), episode); err != nil {
			t.Fatalf("RecordEpisode: %v", err)
		}
	}

	if err := service.postWeeknoteIfDue(context.Background()); err != nil {
		t.Fatalf("postWeeknoteIfDue fallback: %v", err)
	}

	transport := requireFakeTransport(t, service)
	if len(transport.postedTexts) != 1 {
		t.Fatalf("expected fallback weeknote post, got %#v", transport.postedTexts)
	}
	if !strings.Contains(transport.postedTexts[0], "📣 *Rook weeknote*") {
		t.Fatalf("unexpected fallback text %q", transport.postedTexts[0])
	}
	if !strings.Contains(transport.postedTexts[0], "observed 2 agent updates across 2 channels from 2 agents") {
		t.Fatalf("unexpected fallback text %q", transport.postedTexts[0])
	}
	if !strings.Contains(transport.postedTexts[0], "squad0 shipped the planner refactor") {
		t.Fatalf("expected latest highlight in fallback text %q", transport.postedTexts[0])
	}
	if !strings.Contains(service.lastFailureText(), "invalid structured answer") {
		t.Fatalf("expected malformed output to be recorded, got %q", service.lastFailureText())
	}

	episodes, err := service.store.RecentEpisodes(context.Background(), 10)
	if err != nil {
		t.Fatalf("RecentEpisodes: %v", err)
	}
	if !hasWeeknotePost(episodes, weeknoteChannelID, currentTime.Add(-24*time.Hour)) {
		t.Fatalf("expected fallback weeknote episode %#v", episodes)
	}
}

func TestPostWeeknoteIfDueErrorBranches(t *testing.T) {
	t.Parallel()

	currentTime := time.Date(2026, time.April, 3, 10, 5, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return currentTime })
	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.WeeknotesEnabled = true
	service.cfg.Autonomy.WeeknotesChannel = weeknoteChannelID
	service.cfg.Autonomy.WeeknotePostTime = ""

	service.cfg.Service.Timezone = "Bad/Timezone"
	if err := service.postWeeknoteIfDue(context.Background()); err == nil {
		t.Fatal("expected invalid timezone to fail")
	}

	service.cfg.Service.Timezone = "UTC"
	if err := service.store.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}
	if err := service.postWeeknoteIfDue(context.Background()); err == nil {
		t.Fatal("expected closed store to fail")
	}
}

func TestComposeWeeknoteErrorBranches(t *testing.T) {
	t.Parallel()

	currentTime := time.Date(2026, time.April, 3, 10, 5, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return currentTime })
	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.WeeknotesEnabled = true
	service.cfg.Autonomy.WeeknotesChannel = weeknoteChannelID
	service.cfg.Autonomy.WeeknotePostTime = validWeeknoteTime

	service.persona = persona.New(
		service.store,
		service.currentConfig().Persona.CoreConstitutionFile,
		service.currentConfig().Persona.StableIdentitySeed,
		service.currentConfig().Persona.VoiceSeedFile,
		time.Hour,
		service.now,
	)
	if _, err := service.composeWeeknote(context.Background(), service.currentConfig(), nil, currentTime.AddDate(0, 0, -4), currentTime, currentTime); err == nil {
		t.Fatal("expected unseeded persona to fail")
	}

	service.persona = seededPersonaManager(t, service)
	service.ollama = &autonomyOllama{
		fakeOllama: fakeOllama{
			health:    ollama.Health{Reachable: true},
			embedding: []float64{1, 0},
		},
		results: []ollama.ChatResult{
			{Model: "qwen3:4b", Content: "not-json"},
		},
	}
	if _, err := service.composeWeeknote(context.Background(), service.currentConfig(), nil, currentTime.AddDate(0, 0, -4), currentTime, currentTime); err == nil {
		t.Fatal("expected invalid structured weeknote answer to fail")
	}

	service.ollama = &autonomyOllama{
		fakeOllama: fakeOllama{
			health:    ollama.Health{Reachable: true},
			embedding: []float64{1, 0},
		},
		errs: []error{errors.New("chat failed")},
	}
	if _, err := service.composeWeeknote(context.Background(), service.currentConfig(), nil, currentTime.AddDate(0, 0, -4), currentTime, currentTime); err == nil || !strings.Contains(err.Error(), "chat failed") {
		t.Fatalf("expected composeWeeknote chat failure, got %v", err)
	}
}

func TestRunAutonomyLoopDispatchesImmediately(t *testing.T) {
	t.Parallel()

	currentTime := time.Date(2026, time.April, 3, 10, 5, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return currentTime })
	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.WeeknotesEnabled = true
	service.cfg.Autonomy.WeeknotesChannel = weeknoteChannelID
	service.cfg.Autonomy.WeeknotePostTime = validWeeknoteTime
	service.cfg.Autonomy.PollInterval = config.Duration{Duration: 10 * time.Minute}
	service.persona = seededPersonaManager(t, service)
	service.ollama = &autonomyOllama{
		fakeOllama: fakeOllama{
			health:    ollama.Health{Reachable: true},
			embedding: []float64{1, 0},
		},
		results: []ollama.ChatResult{
			{Model: "qwen3:4b", Content: `{"answer":"Weeknote text"}`},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		service.runAutonomyLoop(ctx)
		close(done)
	}()

	transport := requireFakeTransport(t, service)
	deadline := time.Now().Add(time.Second)
	for len(transport.postedTexts) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	if len(transport.postedTexts) != 1 || transport.postedTexts[0] != "Weeknote text" {
		t.Fatalf("expected immediate autonomy post, got %#v", transport.postedTexts)
	}
}

func TestRunAutonomyLoopFailureBranches(t *testing.T) {
	t.Parallel()

	currentTime := time.Date(2026, time.April, 3, 10, 5, 0, 0, time.UTC)
	defaultIntervalService := newTestServiceWithClock(t, func() time.Time { return currentTime })
	defaultIntervalService.cfg.Autonomy.Enabled = true
	defaultIntervalService.cfg.Autonomy.WeeknotesEnabled = true
	defaultIntervalService.cfg.Autonomy.WeeknotesChannel = weeknoteChannelID
	defaultIntervalService.cfg.Autonomy.WeeknotePostTime = invalidClockValue
	defaultIntervalService.cfg.Autonomy.PollInterval = config.Duration{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defaultIntervalService.runAutonomyLoop(ctx)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	if !strings.Contains(defaultIntervalService.lastFailureText(), "invalid weeknote clock") {
		t.Fatalf("expected autonomy loop to record clock failure, got %q", defaultIntervalService.lastFailureText())
	}

	tickingService := newTestServiceWithClock(t, func() time.Time { return currentTime })
	tickingService.cfg.Autonomy.Enabled = true
	tickingService.cfg.Autonomy.WeeknotesEnabled = true
	tickingService.cfg.Autonomy.WeeknotesChannel = weeknoteChannelID
	tickingService.cfg.Autonomy.WeeknotePostTime = invalidClockValue
	tickingService.cfg.Autonomy.PollInterval = config.Duration{Duration: time.Millisecond}

	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() {
		tickingService.runAutonomyLoop(ctx)
		close(done)
	}()

	time.Sleep(5 * time.Millisecond)
	cancel()
	<-done

	if !strings.Contains(tickingService.lastFailureText(), "invalid weeknote clock") {
		t.Fatalf("expected ticker failure to be recorded, got %q", tickingService.lastFailureText())
	}
}

func TestChatAutonomyWithFallbackReturnsNonFallbackErrors(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	service.cfg.Ollama.ChatModel = "primary"
	service.ollama = &autonomyOllama{
		fakeOllama: fakeOllama{
			health:    ollama.Health{Reachable: true},
			embedding: []float64{1, 0},
		},
		errs: []error{errors.New("boom")},
	}

	if _, err := service.chatAutonomyWithFallback(context.Background(), service.currentConfig(), nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected non-fallback error, got %v", err)
	}
}

func seededPersonaManager(t *testing.T, service *Service) *persona.Manager {
	t.Helper()

	manager := persona.New(
		service.store,
		service.currentConfig().Persona.CoreConstitutionFile,
		service.currentConfig().Persona.StableIdentitySeed,
		service.currentConfig().Persona.VoiceSeedFile,
		time.Hour,
		service.now,
	)
	if err := manager.Seed(context.Background()); err != nil {
		t.Fatalf("Seed persona: %v", err)
	}
	if err := manager.Consolidate(context.Background()); err != nil {
		t.Fatalf("Consolidate persona: %v", err)
	}

	return manager
}
