package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Duration supports TOML string durations such as "30s" or "6h".
type Duration struct {
	time.Duration
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = parsed

	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// String returns the duration string.
func (d Duration) String() string {
	if d.Duration == 0 {
		return "0s"
	}

	return d.Duration.String()
}

// ServiceConfig contains process-level settings.
type ServiceConfig struct {
	Name     string `toml:"name"`
	LogLevel string `toml:"log_level"`
	DataDir  string `toml:"data_dir"`
	Timezone string `toml:"timezone"`
}

// SlackConfig configures the Slack transport.
type SlackConfig struct {
	BotToken                  string   `toml:"bot_token"`
	AppToken                  string   `toml:"app_token"`
	AllowDM                   bool     `toml:"allow_dm"`
	MentionRequiredInChannels bool     `toml:"mention_required_in_channels"`
	AllowedChannels           []string `toml:"allowed_channels"`
	DeniedChannels            []string `toml:"denied_channels"`
	MaxConcurrentHandlers     int      `toml:"max_concurrent_handlers"`
}

// OllamaConfig configures the local inference backend.
type OllamaConfig struct {
	Host           string   `toml:"host"`
	ChatModel      string   `toml:"chat_model"`
	ChatFallbacks  []string `toml:"chat_fallback_models"`
	EmbeddingModel string   `toml:"embedding_model"`
	Temperature    float64  `toml:"temperature"`
	ChatTimeout    Duration `toml:"chat_timeout"`
	EmbedTimeout   Duration `toml:"embed_timeout"`
}

// MemoryConfig configures retrieval and persistence.
type MemoryConfig struct {
	DBPath                string   `toml:"db_path"`
	MaxPromptItems        int      `toml:"max_prompt_items"`
	MaxEpisodeItems       int      `toml:"max_episode_items"`
	MaxSearchResults      int      `toml:"max_search_results"`
	EpisodeRetentionDays  int      `toml:"episode_retention_days"`
	ConsolidationInterval Duration `toml:"consolidation_interval"`
	MinWriteImportance    float64  `toml:"min_write_importance"`
	ReminderPollInterval  Duration `toml:"reminder_poll_interval"`
}

// PersonaConfig configures identity file paths.
type PersonaConfig struct {
	CoreConstitutionFile string `toml:"core_constitution_file"`
	StableIdentitySeed   string `toml:"stable_identity_seed_file"`
	VoiceSeedFile        string `toml:"voice_seed_file"`
}

// WebOllamaConfig configures the optional Ollama web provider.
type WebOllamaConfig struct {
	Host   string `toml:"host"`
	APIKey string `toml:"api_key"`
}

// WebConfig configures live web lookup.
type WebConfig struct {
	Enabled         bool            `toml:"enabled"`
	Provider        string          `toml:"provider"`
	Timeout         Duration        `toml:"timeout"`
	MaxResults      int             `toml:"max_results"`
	UserAgent       string          `toml:"user_agent"`
	AutoOnFreshness bool            `toml:"auto_on_freshness"`
	Ollama          WebOllamaConfig `toml:"ollama"`
}

// Squad0Config configures Slack-level observation of squad0 activity.
type Squad0Config struct {
	Enabled         bool     `toml:"enabled"`
	ObservedUserIDs []string `toml:"observed_user_ids"`
	ObservedBotIDs  []string `toml:"observed_bot_ids"`
	Keywords        []string `toml:"keywords"`
}

// AutonomyConfig configures proactive observation and scheduled output.
type AutonomyConfig struct {
	Enabled              bool     `toml:"enabled"`
	ObserveAgentChannels bool     `toml:"observe_agent_channels"`
	WeeknotesEnabled     bool     `toml:"weeknotes_enabled"`
	WeeknotesChannel     string   `toml:"weeknotes_channel"`
	WeeknotePostTime     string   `toml:"weeknote_post_time"`
	PollInterval         Duration `toml:"poll_interval"`
}

// Config is the root application configuration.
type Config struct {
	Service  ServiceConfig  `toml:"service"`
	Slack    SlackConfig    `toml:"slack"`
	Ollama   OllamaConfig   `toml:"ollama"`
	Memory   MemoryConfig   `toml:"memory"`
	Persona  PersonaConfig  `toml:"persona"`
	Web      WebConfig      `toml:"web"`
	Squad0   Squad0Config   `toml:"squad0"`
	Autonomy AutonomyConfig `toml:"autonomy"`
}

// Default returns conservative defaults for local operation.
func Default() Config {
	return Config{
		Service: ServiceConfig{
			Name:     "rook",
			LogLevel: "info",
			DataDir:  "./data",
			Timezone: "UTC",
		},
		Slack: SlackConfig{
			AllowDM:                   true,
			MentionRequiredInChannels: true,
			MaxConcurrentHandlers:     2,
		},
		Ollama: OllamaConfig{
			Host:           "http://127.0.0.1:11434",
			ChatModel:      "qwen3:4b",
			ChatFallbacks:  []string{"phi4-mini"},
			EmbeddingModel: "nomic-embed-text",
			Temperature:    0.7,
			ChatTimeout:    Duration{Duration: 180 * time.Second},
			EmbedTimeout:   Duration{Duration: 30 * time.Second},
		},
		Memory: MemoryConfig{
			DBPath:                "./data/rook.sqlite",
			MaxPromptItems:        8,
			MaxEpisodeItems:       4,
			MaxSearchResults:      12,
			EpisodeRetentionDays:  60,
			ConsolidationInterval: Duration{Duration: 6 * time.Hour},
			MinWriteImportance:    0.6,
			ReminderPollInterval:  Duration{Duration: 30 * time.Second},
		},
		Persona: PersonaConfig{
			CoreConstitutionFile: "./identity/core_constitution.md",
			StableIdentitySeed:   "./identity/stable_identity.md",
			VoiceSeedFile:        "./identity/voice_seed.md",
		},
		Web: WebConfig{
			Provider:        "duckduckgo",
			Timeout:         Duration{Duration: 15 * time.Second},
			MaxResults:      5,
			UserAgent:       "rook/0.1",
			AutoOnFreshness: true,
			Ollama: WebOllamaConfig{
				Host: "https://ollama.com",
			},
		},
		Squad0: Squad0Config{
			Keywords: []string{"squad0"},
		},
		Autonomy: AutonomyConfig{
			ObserveAgentChannels: true,
			WeeknotePostTime:     "10:00",
			PollInterval:         Duration{Duration: time.Minute},
		},
	}
}

// Load reads TOML configuration, applies environment overrides, and normalises paths.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return Config{}, errors.New("config path is required")
	}

	info, err := os.Stat(path)
	if err != nil {
		return Config{}, err
	}
	if info.IsDir() {
		return Config{}, errors.New("config path must be a file")
	}

	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Config{}, err
	}

	applyEnv(&cfg)
	normalise(&cfg)
	resolvePaths(&cfg, filepath.Dir(path))

	if err := validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// Location returns the configured timezone.
func (c Config) Location() (*time.Location, error) {
	return time.LoadLocation(c.Service.Timezone)
}

func applyEnv(cfg *Config) {
	overrideString(&cfg.Service.LogLevel, "ROOK_LOG_LEVEL")
	overrideString(&cfg.Service.DataDir, "ROOK_DATA_DIR")
	overrideString(&cfg.Service.Timezone, "ROOK_TIMEZONE")

	overrideString(&cfg.Slack.BotToken, "ROOK_SLACK_BOT_TOKEN")
	overrideString(&cfg.Slack.AppToken, "ROOK_SLACK_APP_TOKEN")

	overrideString(&cfg.Ollama.Host, "ROOK_OLLAMA_HOST")
	overrideString(&cfg.Ollama.ChatModel, "ROOK_OLLAMA_CHAT_MODEL")
	overrideCSV(&cfg.Ollama.ChatFallbacks, "ROOK_OLLAMA_CHAT_FALLBACK_MODELS")
	overrideString(&cfg.Ollama.EmbeddingModel, "ROOK_OLLAMA_EMBEDDING_MODEL")

	overrideString(&cfg.Memory.DBPath, "ROOK_MEMORY_DB_PATH")

	overrideString(&cfg.Persona.CoreConstitutionFile, "ROOK_PERSONA_CORE_FILE")
	overrideString(&cfg.Persona.StableIdentitySeed, "ROOK_PERSONA_STABLE_FILE")
	overrideString(&cfg.Persona.VoiceSeedFile, "ROOK_PERSONA_VOICE_FILE")

	overrideString(&cfg.Web.Provider, "ROOK_WEB_PROVIDER")
	overrideString(&cfg.Web.Ollama.APIKey, "ROOK_WEB_OLLAMA_API_KEY")
	overrideString(&cfg.Autonomy.WeeknotesChannel, "ROOK_AUTONOMY_WEEKNOTES_CHANNEL")
	overrideString(&cfg.Autonomy.WeeknotePostTime, "ROOK_AUTONOMY_WEEKNOTE_POST_TIME")
}

func normalise(cfg *Config) {
	cfg.Ollama.ChatModel = strings.TrimSpace(cfg.Ollama.ChatModel)
	cfg.Ollama.EmbeddingModel = strings.TrimSpace(cfg.Ollama.EmbeddingModel)
	cfg.Ollama.ChatFallbacks = cleanModelList(cfg.Ollama.ChatFallbacks, cfg.Ollama.ChatModel)
}

func resolvePaths(cfg *Config, baseDir string) {
	cfg.Service.DataDir = resolvePath(baseDir, cfg.Service.DataDir)
	cfg.Memory.DBPath = resolvePath(baseDir, cfg.Memory.DBPath)
	cfg.Persona.CoreConstitutionFile = resolvePath(baseDir, cfg.Persona.CoreConstitutionFile)
	cfg.Persona.StableIdentitySeed = resolvePath(baseDir, cfg.Persona.StableIdentitySeed)
	cfg.Persona.VoiceSeedFile = resolvePath(baseDir, cfg.Persona.VoiceSeedFile)
}

func resolvePath(baseDir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}

	return filepath.Clean(filepath.Join(baseDir, path))
}

func overrideString(target *string, envName string) {
	if value, ok := os.LookupEnv(envName); ok {
		*target = strings.TrimSpace(value)
	}
}

func overrideCSV(target *[]string, envName string) {
	value, ok := os.LookupEnv(envName)
	if !ok {
		return
	}

	*target = splitList(value)
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}

		items = append(items, trimmed)
	}

	return items
}

func cleanModelList(models []string, primary string) []string {
	cleaned := make([]string, 0, len(models))
	seen := map[string]struct{}{}
	primary = strings.TrimSpace(primary)
	if primary != "" {
		seen[strings.ToLower(primary)] = struct{}{}
	}

	for _, model := range models {
		trimmed := strings.TrimSpace(model)
		if trimmed == "" {
			continue
		}

		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		cleaned = append(cleaned, trimmed)
	}

	return cleaned
}

func validate(cfg Config) error {
	switch {
	case cfg.Service.Name == "":
		return errors.New("service.name must not be empty")
	case cfg.Slack.MaxConcurrentHandlers < 1:
		return errors.New("slack.max_concurrent_handlers must be at least 1")
	case cfg.Ollama.Host == "":
		return errors.New("ollama.host must not be empty")
	case cfg.Ollama.ChatModel == "":
		return errors.New("ollama.chat_model must not be empty")
	case cfg.Ollama.EmbeddingModel == "":
		return errors.New("ollama.embedding_model must not be empty")
	case cfg.Memory.DBPath == "":
		return errors.New("memory.db_path must not be empty")
	case cfg.Memory.MaxPromptItems < 1:
		return errors.New("memory.max_prompt_items must be at least 1")
	case cfg.Memory.MaxEpisodeItems < 0:
		return errors.New("memory.max_episode_items must be zero or greater")
	case cfg.Memory.MaxSearchResults < 1:
		return errors.New("memory.max_search_results must be at least 1")
	case cfg.Memory.MinWriteImportance < 0 || cfg.Memory.MinWriteImportance > 1:
		return errors.New("memory.min_write_importance must be between 0 and 1")
	case cfg.Persona.CoreConstitutionFile == "":
		return errors.New("persona.core_constitution_file must not be empty")
	case cfg.Persona.StableIdentitySeed == "":
		return errors.New("persona.stable_identity_seed_file must not be empty")
	case cfg.Persona.VoiceSeedFile == "":
		return errors.New("persona.voice_seed_file must not be empty")
	case cfg.Web.MaxResults < 1:
		return errors.New("web.max_results must be at least 1")
	case cfg.Autonomy.PollInterval.Duration < 0:
		return errors.New("autonomy.poll_interval must be zero or greater")
	case cfg.Autonomy.WeeknotesEnabled && strings.TrimSpace(cfg.Autonomy.WeeknotesChannel) == "":
		return errors.New("autonomy.weeknotes_channel must not be empty when weeknotes are enabled")
	case cfg.Autonomy.WeeknotePostTime != "" && !validClockHHMM(cfg.Autonomy.WeeknotePostTime):
		return errors.New("autonomy.weeknote_post_time must use HH:MM in 24-hour time")
	}

	return nil
}

func validClockHHMM(value string) bool {
	_, _, err := ParseClockHHMM(value)

	return err == nil
}

// ParseClockHHMM parses an HH:MM 24-hour clock value.
func ParseClockHHMM(value string) (hour, minute int, err error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, 0, err
	}

	hour = parsed.Hour()
	minute = parsed.Minute()

	return hour, minute, nil
}
