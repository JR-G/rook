package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunRejectsUnsupportedCommand(t *testing.T) {
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
	if err := os.WriteFile(configPath, []byte(`
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
max_concurrent_handlers = 1

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
reminder_poll_interval = "30s"

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

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"rook", "badcmd", "-config", configPath}

	if err := run(); err == nil {
		t.Fatal("expected unsupported command to fail")
	}
}
