package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JR-G/rook/internal/commands"
	"github.com/JR-G/rook/internal/config"
	"github.com/JR-G/rook/internal/logging"
	"github.com/JR-G/rook/internal/memory"
)

func TestMemoryTextAndFallbackFormattingBranches(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	text, err := service.memoryText(context.Background(), "")
	if err != nil || !strings.Contains(text, "No durable memory") {
		t.Fatalf("unexpected empty recent memory response %q err=%v", text, err)
	}

	text, err = service.memoryText(context.Background(), "missing")
	if err != nil || !strings.Contains(text, "No matching memory") {
		t.Fatalf("unexpected empty memory search response %q err=%v", text, err)
	}

	if _, err := service.store.UpsertMemory(context.Background(), memory.Candidate{
		Type:       memory.Fact,
		Scope:      memory.ScopeUser,
		Subject:    "name",
		Body:       "James",
		Keywords:   []string{"james"},
		Embedding:  []float64{1, 0},
		Confidence: 0.95,
		Importance: 0.9,
	}); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}
	text, err = service.memoryText(context.Background(), "james")
	if err != nil || !strings.Contains(text, "Matching memory") {
		t.Fatalf("unexpected populated memory search response %q err=%v", text, err)
	}

	if got := formatFallbackModels([]string{"phi4-mini", "qwen3:4b"}); got != "phi4-mini, qwen3:4b" {
		t.Fatalf("unexpected fallback formatting %q", got)
	}
}

func TestNewFailsWhenPersonaSeedMissing(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "rook.toml")
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
core_constitution_file = "./missing-core.md"
stable_identity_seed_file = "./missing-stable.md"
voice_seed_file = "./missing-voice.md"

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

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	logger, err := logging.New("error")
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	if _, err := New(cfgPath, cfg, logger); err == nil {
		t.Fatal("expected missing persona seed files to fail")
	}
}

func TestHandleReminderAndDispatchSuccessPaths(t *testing.T) {
	t.Parallel()

	currentTime := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	service := newTestServiceWithClock(t, func() time.Time { return currentTime })

	if _, err := service.handleReminder(context.Background(), slackMessage("remind"), commands.ReminderRequest{
		DueAt:   currentTime.Add(time.Minute),
		Message: "stretch",
	}); err != nil {
		t.Fatalf("handle reminder: %v", err)
	}
	service.now = func() time.Time { return currentTime.Add(2 * time.Minute) }
	if err := service.dispatchDueReminders(context.Background()); err != nil {
		t.Fatalf("dispatch due reminders: %v", err)
	}
}

func TestHandleReminderAndPostLocalCommandFailures(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	if err := service.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if _, err := service.handleReminder(context.Background(), slackMessage("remind"), commands.ReminderRequest{
		DueAt:   time.Now().UTC().Add(time.Minute),
		Message: "stretch",
	}); err == nil {
		t.Fatal("expected handleReminder to fail with closed store")
	}
	if err := service.postLocalCommand(context.Background(), slackMessage("ping"), "ping", "pong"); err == nil {
		t.Fatal("expected postLocalCommand to fail with closed store")
	}
}
