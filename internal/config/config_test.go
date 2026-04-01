package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadResolvesRelativePathsAndEnvOverrides(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "rook.toml")
	content := `
[service]
name = "rook"
log_level = "debug"
data_dir = "./state"
timezone = "UTC"

[slack]
bot_token = "bot-token"
app_token = "app-token"
max_concurrent_handlers = 2

[ollama]
host = "http://127.0.0.1:11434"
chat_model = "phi4-mini"
embedding_model = "nomic-embed-text"
temperature = 0.2
chat_timeout = "1m"
embed_timeout = "30s"

[memory]
db_path = "./state/rook.sqlite"
max_prompt_items = 8
max_episode_items = 4
max_search_results = 12
episode_retention_days = 30
consolidation_interval = "6h"
min_write_importance = 0.6
reminder_poll_interval = "30s"

[persona]
core_constitution_file = "./identity/core.md"
stable_identity_seed_file = "./identity/stable.md"
voice_seed_file = "./identity/voice.md"

[web]
enabled = false
provider = "duckduckgo"
timeout = "15s"
max_results = 5
user_agent = "rook/0.1"
auto_on_freshness = true
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("ROOK_OLLAMA_CHAT_MODEL", "override-model")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Ollama.ChatModel != "override-model" {
		t.Fatalf("expected env override, got %q", cfg.Ollama.ChatModel)
	}
	if cfg.Memory.DBPath != filepath.Join(tempDir, "state", "rook.sqlite") {
		t.Fatalf("expected resolved db path, got %q", cfg.Memory.DBPath)
	}
	if cfg.Persona.CoreConstitutionFile != filepath.Join(tempDir, "identity", "core.md") {
		t.Fatalf("expected resolved persona path, got %q", cfg.Persona.CoreConstitutionFile)
	}
}

func TestValidateRejectsBadConcurrency(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Slack.MaxConcurrentHandlers = 0

	if err := validate(cfg); err == nil {
		t.Fatal("expected validation error")
	}
}
