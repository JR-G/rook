package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JR-G/rook/internal/agent"
	"github.com/JR-G/rook/internal/commands"
	"github.com/JR-G/rook/internal/config"
	"github.com/JR-G/rook/internal/logging"
	"github.com/JR-G/rook/internal/memory"
	"github.com/JR-G/rook/internal/persona"
	slacktransport "github.com/JR-G/rook/internal/slack"
	"github.com/JR-G/rook/internal/tools/web"
)

func TestExecuteCommandVariants(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	service.now = func() time.Time { return time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC) }
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

	if response, err := service.executeCommand(context.Background(), commands.Command{Kind: commands.KindHelp}); err != nil || !strings.Contains(response, "rook commands") {
		t.Fatalf("unexpected help response %q err=%v", response, err)
	}
	if response, err := service.executeCommand(context.Background(), commands.Command{Kind: commands.KindPing}); err != nil || !strings.Contains(response, "pong") {
		t.Fatalf("unexpected ping response %q err=%v", response, err)
	}
	if response, err := service.executeCommand(context.Background(), commands.Command{Kind: commands.KindStatus}); err != nil || !strings.Contains(response, "rook status") {
		t.Fatalf("unexpected status response %q err=%v", response, err)
	}
	if response, err := service.executeCommand(context.Background(), commands.Command{Kind: commands.KindMemory}); err != nil || !strings.Contains(response, "No durable memory") {
		t.Fatalf("unexpected memory response %q err=%v", response, err)
	}
	if response, err := service.executeCommand(context.Background(), commands.Command{Kind: commands.KindModel}); err != nil || !strings.Contains(response, "chat model") {
		t.Fatalf("unexpected model response %q err=%v", response, err)
	}
	if response, err := service.executeCommand(context.Background(), commands.Command{Kind: commands.KindReload}); err != nil || !strings.Contains(response, "persona refreshed") {
		t.Fatalf("unexpected reload response %q err=%v", response, err)
	}
	if response, err := service.executeCommand(context.Background(), commands.Command{Kind: commands.KindRemind}); err != nil || !strings.Contains(response, "Usage:") {
		t.Fatalf("unexpected remind response %q err=%v", response, err)
	}
	if _, err := service.executeCommand(context.Background(), commands.Command{Kind: "unknown"}); err == nil {
		t.Fatal("expected unsupported command to fail")
	}
}

func TestServiceMemoryAndModelHelpers(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	recent, err := service.recentMemoryText(context.Background())
	if err != nil || !strings.Contains(recent, "No durable memory") {
		t.Fatalf("unexpected empty recent memory response %q err=%v", recent, err)
	}

	if _, err := service.store.UpsertMemory(context.Background(), memory.Candidate{
		Type:       memory.Preference,
		Scope:      memory.ScopeUser,
		Subject:    "tone",
		Body:       "Prefer concise replies.",
		Confidence: 0.9,
		Importance: 0.8,
	}); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}
	recent, err = service.recentMemoryText(context.Background())
	if err != nil || !strings.Contains(recent, "Recent durable memory") {
		t.Fatalf("unexpected recent memory response %q err=%v", recent, err)
	}

	modelText := service.modelText("")
	if !strings.Contains(modelText, "chat fallbacks") {
		t.Fatalf("unexpected model text %q", modelText)
	}
	if formatFallbackModels(nil) != "none" {
		t.Fatal("expected empty fallback list to render as none")
	}
	if helpText() == "" {
		t.Fatal("expected help text")
	}
}

func TestRunMessageHandlerAndInputHelpers(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	fake := requireFakeAgent(t, service)
	fake.respondFn = func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{}, context.DeadlineExceeded
	}

	service.runMessageHandler(context.Background(), slackMessage("hello"))
	if !strings.Contains(service.lastFailureText(), "deadline exceeded") {
		t.Fatalf("expected failure text to mention deadline, got %q", service.lastFailureText())
	}
	transport := requireFakeTransport(t, service)
	if len(transport.postedTexts) != 1 || !strings.Contains(transport.postedTexts[0], "warming up") {
		t.Fatalf("unexpected handler posts %#v", transport.postedTexts)
	}

	handled, err := service.handleReminderInput(context.Background(), slackMessage("hello"), "hello", time.UTC)
	if handled || err != nil {
		t.Fatalf("expected non-reminder input to pass through: handled=%t err=%v", handled, err)
	}
	handled, err = service.handleReminderInput(context.Background(), slackMessage("remind me in nope to stretch"), "remind me in nope to stretch", time.UTC)
	if !handled || err != nil {
		t.Fatalf("expected invalid reminder to be handled with user-visible response: handled=%t err=%v", handled, err)
	}
	handled, err = service.handleCommandInput(context.Background(), slackMessage("hello"), "hello")
	if handled || err != nil {
		t.Fatalf("expected plain text to bypass command handling: handled=%t err=%v", handled, err)
	}
	handled, err = service.handleCommandInput(context.Background(), slackMessage("ping"), "ping")
	if !handled || err != nil {
		t.Fatalf("expected ping command to be handled: handled=%t err=%v", handled, err)
	}
}

func TestHandleReminderWritesCommitmentMemory(t *testing.T) {
	t.Parallel()

	service := newTestServiceWithClock(t, func() time.Time {
		return time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	})
	reply, err := service.handleReminder(context.Background(), slackMessage("hello"), commands.ReminderRequest{
		Message: "stretch",
		DueAt:   service.now().Add(30 * time.Minute),
	})
	if err != nil || !strings.Contains(reply, "Reminder set for") {
		t.Fatalf("unexpected reminder reply %q err=%v", reply, err)
	}

	memories, memoryErr := service.store.SearchMemories(context.Background(), "stretch", nil, 5)
	if memoryErr != nil || len(memories) == 0 {
		t.Fatalf("expected reminder commitment memory, got %#v err=%v", memories, memoryErr)
	}
}

func TestBuildSearcherAndNew(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	if _, ok := buildSearcher(cfg).(web.NoopSearcher); !ok {
		t.Fatal("expected disabled web config to build noop searcher")
	}
	cfg.Web.Enabled = true
	if _, ok := buildSearcher(cfg).(*web.DuckDuckGoSearcher); !ok {
		t.Fatal("expected enabled web config to build duckduckgo searcher")
	}

	logger, err := logging.New("error")
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	badCfg := config.Default()
	if _, err := New("config.toml", badCfg, logger); err == nil {
		t.Fatal("expected missing slack tokens to fail")
	}

	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "rook.toml")
	for path, content := range map[string]string{
		filepath.Join(tempDir, "core.md"):   "core",
		filepath.Join(tempDir, "stable.md"): "stable",
		filepath.Join(tempDir, "voice.md"):  "voice",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write persona file: %v", err)
		}
	}
	if err := os.WriteFile(cfgPath, []byte(`
[service]
name = "rook"
log_level = "error"
data_dir = "./data"
timezone = "UTC"

[slack]
bot_token = "bot-token"
app_token = "app-token"
allow_dm = true
mention_required_in_channels = true
allowed_channels = []
denied_channels = []
max_concurrent_handlers = 1

[ollama]
host = "http://127.0.0.1:11434"
chat_model = "qwen3:4b"
chat_fallback_models = ["phi4-mini"]
embedding_model = "nomic-embed-text"
temperature = 0.7
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
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loadedCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	service, err := New(cfgPath, loadedCfg, logger)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if service.currentConfig().Ollama.ChatModel != "qwen3:4b" {
		t.Fatalf("unexpected service config %#v", service.currentConfig().Ollama)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("close service: %v", err)
	}
}

func slackMessage(text string) slacktransport.InboundMessage {
	return slacktransport.InboundMessage{
		ChannelID: "D1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Text:      text,
		IsDM:      true,
	}
}
