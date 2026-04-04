package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadErrors(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	if _, err := Load(""); err == nil {
		t.Fatal("expected empty path to fail")
	}
	if _, err := Load(tempDir); err == nil {
		t.Fatal("expected directory path to fail")
	}

	badTOMLPath := filepath.Join(tempDir, "bad.toml")
	if err := os.WriteFile(badTOMLPath, []byte("[service"), 0o600); err != nil {
		t.Fatalf("write bad toml: %v", err)
	}
	if _, err := Load(badTOMLPath); err == nil {
		t.Fatal("expected bad toml to fail")
	}

	invalidPath := filepath.Join(tempDir, "invalid.toml")
	if err := os.WriteFile(invalidPath, []byte(`
[service]
name = ""
log_level = "info"
data_dir = "./data"
timezone = "UTC"

[slack]
bot_token = "bot-token"
app_token = "app-token"
max_concurrent_handlers = 1

[ollama]
host = "http://127.0.0.1:11434"
chat_model = ""
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
		t.Fatalf("write invalid config: %v", err)
	}

	if _, err := Load(invalidPath); err == nil || !strings.Contains(err.Error(), "service.name") {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestConfigHelperFunctions(t *testing.T) {
	t.Parallel()

	if got := splitList(" a, ,b,, c "); len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("unexpected split list %#v", got)
	}

	if got := resolvePath("/tmp/rook", "data/rook.sqlite"); got != filepath.Clean("/tmp/rook/data/rook.sqlite") {
		t.Fatalf("unexpected relative path %q", got)
	}
	if got := resolvePath("/tmp/rook", "/var/tmp/rook.sqlite"); got != "/var/tmp/rook.sqlite" {
		t.Fatalf("unexpected absolute path %q", got)
	}

	cleaned := cleanModelList([]string{" phi4-mini ", "QWEN3:4B", "phi4-mini", ""}, "qwen3:4b")
	if len(cleaned) != 1 || cleaned[0] != "phi4-mini" {
		t.Fatalf("unexpected cleaned models %#v", cleaned)
	}

	cfg := Default()
	cfg.Ollama.ChatModel = " qwen3:4b "
	cfg.Ollama.EmbeddingModel = " nomic-embed-text "
	cfg.Ollama.ChatFallbacks = []string{" phi4-mini ", "qwen3:4b"}
	normalise(&cfg)
	if cfg.Ollama.ChatModel != "qwen3:4b" || cfg.Ollama.EmbeddingModel != "nomic-embed-text" {
		t.Fatalf("unexpected normalised models %#v", cfg.Ollama)
	}
	if len(cfg.Ollama.ChatFallbacks) != 1 || cfg.Ollama.ChatFallbacks[0] != "phi4-mini" {
		t.Fatalf("unexpected fallbacks %#v", cfg.Ollama.ChatFallbacks)
	}
}

func TestValidateRejectsInvalidFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "empty host", mutate: func(cfg *Config) { cfg.Ollama.Host = "" }, want: "ollama.host"},
		{name: "empty chat model", mutate: func(cfg *Config) { cfg.Ollama.ChatModel = "" }, want: "ollama.chat_model"},
		{name: "empty embedding model", mutate: func(cfg *Config) { cfg.Ollama.EmbeddingModel = "" }, want: "ollama.embedding_model"},
		{name: "empty db path", mutate: func(cfg *Config) { cfg.Memory.DBPath = "" }, want: "memory.db_path"},
		{name: "bad prompt items", mutate: func(cfg *Config) { cfg.Memory.MaxPromptItems = 0 }, want: "memory.max_prompt_items"},
		{name: "bad episode items", mutate: func(cfg *Config) { cfg.Memory.MaxEpisodeItems = -1 }, want: "memory.max_episode_items"},
		{name: "bad search results", mutate: func(cfg *Config) { cfg.Memory.MaxSearchResults = 0 }, want: "memory.max_search_results"},
		{name: "bad importance", mutate: func(cfg *Config) { cfg.Memory.MinWriteImportance = 2 }, want: "memory.min_write_importance"},
		{name: "missing core file", mutate: func(cfg *Config) { cfg.Persona.CoreConstitutionFile = "" }, want: "persona.core_constitution_file"},
		{name: "missing stable file", mutate: func(cfg *Config) { cfg.Persona.StableIdentitySeed = "" }, want: "persona.stable_identity_seed_file"},
		{name: "missing voice file", mutate: func(cfg *Config) { cfg.Persona.VoiceSeedFile = "" }, want: "persona.voice_seed_file"},
		{name: "bad web max", mutate: func(cfg *Config) { cfg.Web.MaxResults = 0 }, want: "web.max_results"},
	}

	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := Default()
			testCase.mutate(&cfg)
			err := validate(cfg)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("expected %q error, got %v", testCase.want, err)
			}
		})
	}
}

func TestDurationUnmarshalErrorAndZeroString(t *testing.T) {
	t.Parallel()

	var duration Duration
	if err := duration.UnmarshalText([]byte("bad")); err == nil {
		t.Fatal("expected bad duration to fail")
	}
	if duration.String() != "0s" {
		t.Fatalf("unexpected zero duration string %q", duration.String())
	}
}
