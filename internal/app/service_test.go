package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JR-G/rook/internal/agent"
	"github.com/JR-G/rook/internal/config"
	"github.com/JR-G/rook/internal/integrations/squad0"
	"github.com/JR-G/rook/internal/logging"
	"github.com/JR-G/rook/internal/memory"
	"github.com/JR-G/rook/internal/ollama"
	"github.com/JR-G/rook/internal/persona"
	slacktransport "github.com/JR-G/rook/internal/slack"
	"github.com/JR-G/rook/internal/tools/web"
)

type fakeAgent struct {
	respondFn       func(context.Context, agent.Request) (agent.Response, error)
	chatModel       string
	embeddingModel  string
	updateCfg       agent.Config
	updateSearcher  web.Searcher
	setChatModelTo  string
	respondRequests []agent.Request
}

func (f *fakeAgent) Respond(ctx context.Context, request agent.Request) (agent.Response, error) {
	f.respondRequests = append(f.respondRequests, request)
	if f.respondFn != nil {
		return f.respondFn(ctx, request)
	}

	return agent.Response{Text: "ok"}, nil
}

func (f *fakeAgent) SetChatModel(model string) {
	f.setChatModelTo = model
	f.chatModel = model
}

func (f *fakeAgent) ChatModel() string {
	return f.chatModel
}

func (f *fakeAgent) EmbeddingModel() string {
	return f.embeddingModel
}

func (f *fakeAgent) UpdateConfig(cfg agent.Config, searcher web.Searcher) {
	f.updateCfg = cfg
	f.updateSearcher = searcher
}

type fakeOllama struct {
	health    ollama.Health
	healthErr error
	embedding []float64
	embedErr  error
}

func (f fakeOllama) Health(context.Context) (ollama.Health, error) {
	return f.health, f.healthErr
}

func (f fakeOllama) Embed(context.Context, string, string) ([]float64, error) {
	return f.embedding, f.embedErr
}

type fakeTransport struct {
	runErr      error
	status      slacktransport.Status
	handler     func(context.Context, slacktransport.InboundMessage)
	postMu      sync.Mutex
	postedTexts []string
}

func (f *fakeTransport) Run(ctx context.Context, handler func(context.Context, slacktransport.InboundMessage)) error {
	f.handler = handler
	if f.runErr != nil {
		return f.runErr
	}
	<-ctx.Done()

	return ctx.Err()
}

func (f *fakeTransport) PostMessage(_ context.Context, _, _, text string) error {
	f.postMu.Lock()
	defer f.postMu.Unlock()
	f.postedTexts = append(f.postedTexts, text)

	return nil
}

func (f *fakeTransport) Status() slacktransport.Status {
	return f.status
}

func requireFakeTransport(t *testing.T, service *Service) *fakeTransport {
	t.Helper()

	transport, ok := service.transport.(*fakeTransport)
	if !ok {
		t.Fatalf("expected fake transport, got %T", service.transport)
	}

	return transport
}

func requireFakeAgent(t *testing.T, service *Service) *fakeAgent {
	t.Helper()

	fake, ok := service.agent.(*fakeAgent)
	if !ok {
		t.Fatalf("expected fake agent, got %T", service.agent)
	}

	return fake
}

func TestProcessMessageCommandAndMemory(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	if _, err := service.store.UpsertMemory(context.Background(), memory.Candidate{
		Type:       memory.Fact,
		Scope:      memory.ScopeUser,
		Subject:    "name",
		Body:       "Preferred name is Rook User",
		Keywords:   []string{"name"},
		Confidence: 0.95,
		Importance: 0.9,
	}); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}

	if err := service.processMessage(context.Background(), slacktransport.InboundMessage{
		ChannelID: "D1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Text:      "memory name",
		IsDM:      true,
	}); err != nil {
		t.Fatalf("process message: %v", err)
	}

	transport := requireFakeTransport(t, service)
	if len(transport.postedTexts) != 1 {
		t.Fatalf("expected one post, got %d", len(transport.postedTexts))
	}
	if !strings.Contains(transport.postedTexts[0], "Matching memory") {
		t.Fatalf("unexpected memory output %q", transport.postedTexts[0])
	}
}

func TestProcessMessageGeneralConversation(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	fake := requireFakeAgent(t, service)
	fake.respondFn = func(_ context.Context, request agent.Request) (agent.Response, error) {
		if request.Text != "hello" {
			t.Fatalf("unexpected request text %q", request.Text)
		}

		return agent.Response{Text: "reply"}, nil
	}

	if err := service.processMessage(context.Background(), slacktransport.InboundMessage{
		ChannelID: "D1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Text:      "hello",
		IsDM:      true,
	}); err != nil {
		t.Fatalf("process message: %v", err)
	}

	transport := requireFakeTransport(t, service)
	if len(transport.postedTexts) != 1 || transport.postedTexts[0] != "reply" {
		t.Fatalf("unexpected posts %#v", transport.postedTexts)
	}
}

func TestProcessMessageReminderAndDispatch(t *testing.T) {
	t.Parallel()

	currentTime := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return currentTime })

	if err := service.processMessage(context.Background(), slacktransport.InboundMessage{
		ChannelID: "D1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Text:      "remind me in 1m to stretch",
		IsDM:      true,
	}); err != nil {
		t.Fatalf("process reminder: %v", err)
	}

	transport := requireFakeTransport(t, service)
	if len(transport.postedTexts) != 1 || !strings.Contains(transport.postedTexts[0], "Reminder set") {
		t.Fatalf("unexpected reminder set output %#v", transport.postedTexts)
	}

	service.now = func() time.Time { return currentTime.Add(2 * time.Minute) }
	if err := service.dispatchDueReminders(context.Background()); err != nil {
		t.Fatalf("dispatch reminders: %v", err)
	}

	if len(transport.postedTexts) != 2 || !strings.Contains(transport.postedTexts[1], "Reminder") {
		t.Fatalf("unexpected reminder dispatch output %#v", transport.postedTexts)
	}
}

func TestProcessMessageObservesSquad0WithoutReply(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	service.observer = squad0ObserverForTest()

	if err := service.processMessage(context.Background(), slacktransport.InboundMessage{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		UserID:    "U-SQUAD",
		Text:      "squad0 update",
		IsDM:      false,
		Mentioned: false,
	}); err != nil {
		t.Fatalf("process observation: %v", err)
	}

	episodes, err := service.store.RecentEpisodes(context.Background(), 5)
	if err != nil {
		t.Fatalf("recent episodes: %v", err)
	}
	if len(episodes) != 1 || episodes[0].Source != "squad0" {
		t.Fatalf("unexpected observed episodes %#v", episodes)
	}
	transport := requireFakeTransport(t, service)
	if len(transport.postedTexts) != 0 {
		t.Fatal("expected no Slack reply for passive observation")
	}
}

func TestHandleInboundAndRun(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	transport := requireFakeTransport(t, service)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- service.Run(ctx)
	}()

	if transport.handler == nil {
		time.Sleep(20 * time.Millisecond)
	}
	transport.handler(context.Background(), slacktransport.InboundMessage{
		ChannelID: "D1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Text:      "ping",
		IsDM:      true,
	})

	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled run, got %v", err)
	}
	if len(transport.postedTexts) == 0 {
		t.Fatal("expected inbound handler to post a reply")
	}
}

func TestStatusReloadAndHelpers(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	status, err := service.statusText(context.Background())
	if err != nil {
		t.Fatalf("status text: %v", err)
	}
	if !strings.Contains(status, "rook status") {
		t.Fatalf("unexpected status text %q", status)
	}

	service.recordFailure(errors.New("boom"))
	if !strings.Contains(service.lastFailureText(), "boom") {
		t.Fatal("expected failure text to include error")
	}
	if !strings.Contains(service.modelText("set phi4-mini"), "phi4-mini") {
		t.Fatal("expected model text to mention chat model")
	}
	if !contains([]string{"a", "b"}, "b") {
		t.Fatal("expected contains helper to succeed")
	}
	if formatTime(time.Time{}) != "never" {
		t.Fatal("expected zero time format")
	}

	reply := service.reload()
	if !strings.Contains(reply, "configuration reloaded") {
		t.Fatalf("unexpected reload reply %q", reply)
	}
}

func TestReloadRefreshesPersonaWhenPresent(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	personaManager := persona.New(
		service.store,
		service.currentConfig().Persona.CoreConstitutionFile,
		service.currentConfig().Persona.StableIdentitySeed,
		service.currentConfig().Persona.VoiceSeedFile,
		time.Hour,
		service.now,
	)
	if err := personaManager.Seed(context.Background()); err != nil {
		t.Fatalf("seed persona: %v", err)
	}
	service.persona = personaManager

	reply := service.reload()
	if !strings.Contains(reply, "persona refreshed from seed files") {
		t.Fatalf("unexpected persona reload reply %q", reply)
	}
}

func TestNormaliseTextAndHandleInboundCapacity(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	transport := requireFakeTransport(t, service)
	transport.status = slacktransport.Status{BotUserID: "UROOK"}
	if got := service.normaliseText("<@UROOK> hello"); got != "hello" {
		t.Fatalf("unexpected normalised text %q", got)
	}

	service.sem = make(chan struct{}, 1)
	service.sem <- struct{}{}
	service.HandleInbound(context.Background(), slacktransport.InboundMessage{
		ChannelID: "D1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Text:      "ping",
		IsDM:      true,
	})
	if !strings.Contains(service.lastFailureText(), "capacity") {
		t.Fatalf("expected capacity failure, got %q", service.lastFailureText())
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()

	return newTestServiceWithClock(t, time.Now)
}

func newTestServiceWithClock(t *testing.T, clock func() time.Time) *Service {
	t.Helper()

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "rook.toml")
	for path, content := range map[string]string{
		filepath.Join(tempDir, "core.md"):   "core",
		filepath.Join(tempDir, "stable.md"): "stable",
		filepath.Join(tempDir, "voice.md"):  "voice",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write persona file: %v", err)
		}
	}
	configContents := `
[service]
name = "rook"
log_level = "info"
data_dir = "./data"
timezone = "UTC"

[slack]
bot_token = "bot-token"
app_token = "app-token"
allow_dm = true
mention_required_in_channels = true
allowed_channels = []
denied_channels = []
max_concurrent_handlers = 2

[ollama]
host = "http://127.0.0.1:11434"
chat_model = "phi4-mini"
embedding_model = "nomic-embed-text"
temperature = 0.2
chat_timeout = "1s"
embed_timeout = "1s"

[memory]
db_path = "./rook.sqlite"
max_prompt_items = 4
max_episode_items = 2
max_search_results = 8
episode_retention_days = 30
consolidation_interval = "1h"
min_write_importance = 0.6
reminder_poll_interval = "10ms"

[persona]
core_constitution_file = "./core.md"
stable_identity_seed_file = "./stable.md"
voice_seed_file = "./voice.md"

[web]
enabled = false
provider = "duckduckgo"
timeout = "5s"
max_results = 2
user_agent = "rook-test"
auto_on_freshness = true

[squad0]
enabled = false
observed_user_ids = ["U-SQUAD"]
observed_bot_ids = []
keywords = ["squad0"]
`
	if err := os.WriteFile(configPath, []byte(configContents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	store, err := memory.OpenWithClock(cfg.Memory.DBPath, clock)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	logger, err := logging.New("error")
	if err != nil {
		t.Fatalf("logger: %v", err)
	}

	service := &Service{
		cfgPath: configPath,
		logger:  logger,
		started: clock().UTC().Add(-time.Minute),
		now:     clock,
		cfg:     cfg,
		store:   store,
		ollama: fakeOllama{
			health:    ollama.Health{Reachable: true, Models: []string{"phi4-mini"}},
			embedding: []float64{1, 0},
		},
		agent: &fakeAgent{
			chatModel:      "phi4-mini",
			embeddingModel: "nomic-embed-text",
		},
		transport: &fakeTransport{},
		observer:  squad0ObserverForTest(),
		sem:       make(chan struct{}, 2),
	}

	return service
}

func squad0ObserverForTest() squad0.Observer {
	return squad0.New(squad0.Config{
		Enabled:         true,
		ObservedUserIDs: []string{"U-SQUAD"},
		Keywords:        []string{"squad0"},
	})
}
