package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JR-G/rook/internal/memory"
	"github.com/JR-G/rook/internal/ollama"
	slacktransport "github.com/JR-G/rook/internal/slack"
)

type autonomyOllama struct {
	fakeOllama
	results []ollama.ChatResult
	errs    []error
	models  []string
	prompts []string
}

func (client *autonomyOllama) ChatStructured(
	_ context.Context,
	model string,
	messages []ollama.Message,
	_ float64,
	_ any,
) (ollama.ChatResult, error) {
	client.models = append(client.models, model)
	if len(messages) > 0 {
		client.prompts = append(client.prompts, messages[len(messages)-1].Content)
	}
	index := len(client.models) - 1
	if index < len(client.errs) && client.errs[index] != nil {
		return ollama.ChatResult{}, client.errs[index]
	}
	if index < len(client.results) {
		return client.results[index], nil
	}

	return ollama.ChatResult{
		Model:   model,
		Content: `{"answer":"Weeknote text"}`,
	}, nil
}

func TestObserveAmbientActivityRecordsAgentMessages(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.ObserveAgentChannels = true

	transport := requireFakeTransport(t, service)
	transport.status = slacktransport.Status{BotUserID: "U-ROOK", BotID: "B-ROOK"}

	observed, err := service.observeAmbientActivity(context.Background(), slacktransport.InboundMessage{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		BotID:     "B-OTHER",
		Text:      "agent posted an update",
	}, false)
	if err != nil {
		t.Fatalf("observeAmbientActivity: %v", err)
	}
	if !observed {
		t.Fatal("expected ambient activity to be observed")
	}

	episodes, err := service.store.RecentEpisodes(context.Background(), 5)
	if err != nil {
		t.Fatalf("RecentEpisodes: %v", err)
	}
	if len(episodes) != 1 {
		t.Fatalf("unexpected episode count %#v", episodes)
	}
	if episodes[0].Source != sourceAmbientAgent || episodes[0].UserID != "B-OTHER" {
		t.Fatalf("unexpected observed episode %#v", episodes[0])
	}
}

func TestObserveAmbientActivitySkipsUnsafeCases(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	service.cfg.Autonomy.Enabled = true
	service.cfg.Autonomy.ObserveAgentChannels = true

	transport := requireFakeTransport(t, service)
	transport.status = slacktransport.Status{BotUserID: "U-ROOK", BotID: "B-ROOK"}

	cases := []slacktransport.InboundMessage{
		{ChannelID: "C1", ThreadTS: "1.0", BotID: "B-OTHER", Text: "reply already handled"},
		{ChannelID: "C1", ThreadTS: "1.0", BotID: "B-OTHER", Text: "dm", IsDM: true},
		{ChannelID: "C1", ThreadTS: "1.0", BotID: "B-ROOK", Text: "rook self message"},
		{ChannelID: "C1", ThreadTS: "1.0", UserID: "U-ROOK", BotID: "B-OTHER", Text: "rook echo"},
	}
	flags := []bool{true, false, false, false}

	for index, message := range cases {
		observed, err := service.observeAmbientActivity(context.Background(), message, flags[index])
		if err != nil {
			t.Fatalf("case %d observeAmbientActivity: %v", index, err)
		}
		if observed {
			t.Fatalf("case %d unexpectedly observed %#v", index, message)
		}
	}

	episodes, err := service.store.RecentEpisodes(context.Background(), 5)
	if err != nil {
		t.Fatalf("RecentEpisodes: %v", err)
	}
	if len(episodes) != 0 {
		t.Fatalf("expected no stored observations %#v", episodes)
	}
}

func TestRecordObservedEpisodeError(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	if err := service.store.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	observed, err := service.recordObservedEpisode(context.Background(), slacktransport.InboundMessage{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		BotID:     "B-OTHER",
		Text:      "agent posted an update",
	}, sourceAmbientAgent)
	if err == nil {
		t.Fatal("expected recordObservedEpisode to fail with closed store")
	}
	if observed {
		t.Fatal("did not expect recordObservedEpisode to report success")
	}
}

func TestShouldRespondSkipsUnmentionedBotThreadMessages(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	if _, err := service.store.RecordEpisode(context.Background(), memory.EpisodeInput{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		UserID:    "rook",
		Role:      "assistant",
		Source:    "assistant",
		Text:      "Earlier reply",
	}); err != nil {
		t.Fatalf("RecordEpisode: %v", err)
	}

	shouldRespond, err := service.shouldRespond(context.Background(), slacktransport.InboundMessage{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		BotID:     "B-OTHER",
		Text:      "follow-up from another bot",
		IsDM:      false,
		Mentioned: false,
	})
	if err != nil {
		t.Fatalf("shouldRespond: %v", err)
	}
	if shouldRespond {
		t.Fatal("expected unmentioned bot follow-up to be observed only")
	}
}

func TestWeeknoteWindow(t *testing.T) {
	t.Parallel()

	fridayMorning := time.Date(2026, time.April, 3, 9, 59, 0, 0, time.UTC)
	weekStart, scheduledAt, due, err := weeknoteWindow(fridayMorning, "10:00")
	if err != nil {
		t.Fatalf("weeknoteWindow before schedule: %v", err)
	}
	if due {
		t.Fatal("did not expect weeknote to be due before schedule")
	}
	if weekStart != time.Date(2026, time.March, 30, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("unexpected week start %s", weekStart)
	}
	if scheduledAt != time.Date(2026, time.April, 3, 10, 0, 0, 0, time.UTC) {
		t.Fatalf("unexpected scheduled time %s", scheduledAt)
	}

	_, _, due, err = weeknoteWindow(fridayMorning.Add(2*time.Minute), "10:00")
	if err != nil {
		t.Fatalf("weeknoteWindow after schedule: %v", err)
	}
	if !due {
		t.Fatal("expected weeknote to be due after schedule")
	}

	if _, _, _, err := weeknoteWindow(fridayMorning, "25:00"); err == nil {
		t.Fatal("expected invalid clock to fail")
	}
}

func TestWeeknoteHelpers(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.April, 3, 10, 5, 0, 0, time.UTC)
	episodes := []memory.Episode{
		{ChannelID: "C1", UserID: "B1", Source: sourceAmbientAgent, Summary: "most recent", CreatedAt: base},
		{ChannelID: "C2", UserID: "U-SQUAD", Source: "squad0", Summary: "slightly older", CreatedAt: base.Add(-time.Minute)},
		{ChannelID: "C3", UserID: "B2", Source: "assistant", Summary: "ignored", CreatedAt: base.Add(-2 * time.Minute)},
		{ChannelID: "C4", UserID: "B3", Source: sourceAmbientAgent, Summary: "too old", CreatedAt: base.AddDate(0, 0, -8)},
	}

	filtered := observedAgentEpisodes(episodes, base.Add(-2*time.Minute), base.Add(time.Second))
	if len(filtered) != 2 {
		t.Fatalf("unexpected filtered episodes %#v", filtered)
	}
	if filtered[0].Summary != "slightly older" || filtered[1].Summary != "most recent" {
		t.Fatalf("expected chronological filtered episodes %#v", filtered)
	}

	if rendered := formatWeeknoteEpisodes(nil); rendered != "- none" {
		t.Fatalf("unexpected empty weeknote render %q", rendered)
	}
	rendered := formatWeeknoteEpisodes(filtered)
	if !strings.Contains(rendered, "actor=B1") || !strings.Contains(rendered, "summary=slightly older") {
		t.Fatalf("unexpected rendered weeknote episodes %q", rendered)
	}

	prompt := buildWeeknotePrompt(filtered, base.AddDate(0, 0, -4), base, base.Add(5*time.Minute))
	if !strings.Contains(prompt, "Sound like rook") || !strings.Contains(prompt, "Observed agent activity") {
		t.Fatalf("unexpected weeknote prompt %q", prompt)
	}

	if hasWeeknotePost(filtered, "C-WEEK", base.Add(-time.Hour)) {
		t.Fatal("did not expect unrelated channel to count as weeknote")
	}
	filtered = append(filtered, memory.Episode{
		ChannelID: "C-WEEK",
		Source:    sourceWeeknote,
		CreatedAt: base,
	})
	if !hasWeeknotePost(filtered, "C-WEEK", base.Add(-time.Hour)) {
		t.Fatal("expected weeknote post detection")
	}

	manyEpisodes := make([]memory.Episode, 0, weeknoteEventLimit+1)
	for index := 0; index < weeknoteEventLimit+1; index++ {
		manyEpisodes = append(manyEpisodes, memory.Episode{
			ChannelID: "C1",
			Source:    sourceAmbientAgent,
			Summary:   "kept",
			CreatedAt: base.Add(time.Duration(index) * time.Second),
		})
	}
	if len(observedAgentEpisodes(manyEpisodes, base.Add(-time.Hour), base.Add(2*time.Hour))) != weeknoteEventLimit {
		t.Fatalf("expected observedAgentEpisodes to cap at %d", weeknoteEventLimit)
	}
}

func TestChatAutonomyWithFallback(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	service.cfg.Ollama.ChatModel = "primary"
	service.cfg.Ollama.ChatFallbacks = []string{"fallback"}
	service.ollama = &autonomyOllama{
		fakeOllama: fakeOllama{
			health:    ollama.Health{Reachable: true},
			embedding: []float64{1, 0},
		},
		errs: []error{
			ollama.StatusError{StatusCode: 404, Message: "model not found"},
		},
		results: []ollama.ChatResult{
			{},
			{Model: "fallback", Content: `{"answer":"fallback worked"}`},
		},
	}

	result, err := service.chatAutonomyWithFallback(context.Background(), service.currentConfig(), []ollama.Message{
		{Role: "user", Content: "compose"},
	})
	if err != nil {
		t.Fatalf("chatAutonomyWithFallback: %v", err)
	}
	if result.Model != "fallback" {
		t.Fatalf("expected fallback model, got %#v", result)
	}

	service.cfg.Ollama.ChatModel = ""
	service.cfg.Ollama.ChatFallbacks = nil
	if _, err := service.chatAutonomyWithFallback(context.Background(), service.currentConfig(), nil); err == nil || !strings.Contains(err.Error(), "no chat model configured") {
		t.Fatalf("expected no-model error, got %v", err)
	}
}
