package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/JR-G/rook/internal/agent"
	"github.com/JR-G/rook/internal/commands"
	"github.com/JR-G/rook/internal/config"
	"github.com/JR-G/rook/internal/integrations/squad0"
	"github.com/JR-G/rook/internal/memory"
	"github.com/JR-G/rook/internal/ollama"
	"github.com/JR-G/rook/internal/output"
	"github.com/JR-G/rook/internal/persona"
	slacktransport "github.com/JR-G/rook/internal/slack"
	"github.com/JR-G/rook/internal/tools/web"
)

// Service wires the persistent runtime together.
type Service struct {
	cfgPath string
	logger  *slog.Logger
	started time.Time
	now     func() time.Time

	mu          sync.RWMutex
	cfg         config.Config
	lastFailure string
	lastFailed  time.Time

	store     *memory.Store
	ollama    ollamaClient
	persona   *persona.Manager
	agent     agentClient
	transport slackClient
	observer  squad0.Observer
	sem       chan struct{}
}

type agentClient interface {
	Respond(context.Context, agent.Request) (agent.Response, error)
	SetChatModel(string)
	ChatModel() string
	EmbeddingModel() string
	UpdateConfig(agent.Config, web.Searcher)
}

type ollamaClient interface {
	Health(context.Context) (ollama.Health, error)
	Embed(context.Context, string, string) ([]float64, error)
}

type slackClient interface {
	Run(context.Context, func(context.Context, slacktransport.InboundMessage)) error
	PostMessage(context.Context, string, string, string) error
	Status() slacktransport.Status
}

// New creates a new runnable service.
func New(cfgPath string, cfg config.Config, logger *slog.Logger) (*Service, error) {
	if strings.TrimSpace(cfg.Slack.BotToken) == "" || strings.TrimSpace(cfg.Slack.AppToken) == "" {
		return nil, errors.New("slack bot and app tokens are required")
	}

	now := time.Now
	store, err := memory.Open(cfg.Memory.DBPath)
	if err != nil {
		return nil, err
	}

	personaManager := persona.New(
		store,
		cfg.Persona.CoreConstitutionFile,
		cfg.Persona.StableIdentitySeed,
		cfg.Persona.VoiceSeedFile,
		cfg.Memory.ConsolidationInterval.Duration,
		now,
	)
	if err := personaManager.Seed(context.Background()); err != nil {
		_ = store.Close()
		return nil, err
	}

	ollamaClient := ollama.New(
		cfg.Ollama.Host,
		cfg.Ollama.ChatTimeout.Duration,
		cfg.Ollama.EmbedTimeout.Duration,
	)

	return &Service{
		cfgPath:   cfgPath,
		logger:    logger,
		started:   now().UTC(),
		now:       now,
		cfg:       cfg,
		store:     store,
		ollama:    ollamaClient,
		persona:   personaManager,
		agent:     newAgentService(cfg, ollamaClient, store, personaManager),
		transport: slacktransport.New(cfg.Slack.BotToken, cfg.Slack.AppToken, logger),
		observer:  newSquad0Observer(cfg),
		sem:       make(chan struct{}, cfg.Slack.MaxConcurrentHandlers),
	}, nil
}

func newAgentService(
	cfg config.Config,
	ollamaClient *ollama.Client,
	store *memory.Store,
	personaManager *persona.Manager,
) *agent.Service {
	return agent.New(
		ollamaClient,
		store,
		personaManager,
		buildSearcher(cfg),
		output.New(),
		agent.Config{
			ChatModel:          cfg.Ollama.ChatModel,
			ChatFallbacks:      cfg.Ollama.ChatFallbacks,
			EmbeddingModel:     cfg.Ollama.EmbeddingModel,
			Temperature:        cfg.Ollama.Temperature,
			MinWriteImportance: cfg.Memory.MinWriteImportance,
			MaxPromptItems:     cfg.Memory.MaxPromptItems,
			MaxEpisodeItems:    cfg.Memory.MaxEpisodeItems,
			WebEnabled:         cfg.Web.Enabled,
			WebMaxResults:      cfg.Web.MaxResults,
			AutoOnFreshness:    cfg.Web.AutoOnFreshness,
		},
	)
}

func newSquad0Observer(cfg config.Config) squad0.Observer {
	return squad0.New(squad0.Config{
		Enabled:         cfg.Squad0.Enabled,
		ObservedUserIDs: cfg.Squad0.ObservedUserIDs,
		ObservedBotIDs:  cfg.Squad0.ObservedBotIDs,
		Keywords:        cfg.Squad0.Keywords,
	})
}

// Close shuts down local resources.
func (s *Service) Close() error {
	return s.store.Close()
}

// Run starts the reminder loop and Slack transport.
func (s *Service) Run(ctx context.Context) error {
	go s.runReminderLoop(ctx)

	err := s.transport.Run(ctx, s.HandleInbound)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.recordFailure(err)
	}

	return err
}

// HandleInbound accepts one Slack event and schedules its processing.
func (s *Service) HandleInbound(ctx context.Context, message slacktransport.InboundMessage) {
	select {
	case s.sem <- struct{}{}:
	default:
		s.logger.Warn("message handler capacity reached")
		s.recordFailure(fmt.Errorf("message handler capacity reached"))
		return
	}

	go func() {
		defer func() { <-s.sem }()
		s.runMessageHandler(ctx, message)
	}()
}

func (s *Service) processMessage(ctx context.Context, message slacktransport.InboundMessage) error {
	observed, err := s.observeSquad0(ctx, message)
	if err != nil {
		return err
	}
	if observed && !s.shouldRespond(message) {
		return nil
	}
	if !s.shouldRespond(message) {
		return nil
	}

	text := s.normaliseText(message.Text)
	if text == "" {
		return nil
	}

	location, err := s.cfg.Location()
	if err != nil {
		return err
	}

	handled, err := s.handleReminderInput(ctx, message, text, location)
	if handled || err != nil {
		return err
	}

	handled, err = s.handleCommandInput(ctx, message, text)
	if handled || err != nil {
		return err
	}

	response, err := s.agent.Respond(ctx, agent.Request{
		ChannelID: message.ChannelID,
		ThreadTS:  message.ThreadTS,
		UserID:    message.UserID,
		Text:      text,
	})
	if err != nil {
		return err
	}

	if err := s.store.PruneEpisodes(ctx, s.cfg.Memory.EpisodeRetentionDays); err != nil {
		return err
	}

	return s.transport.PostMessage(ctx, message.ChannelID, message.ThreadTS, response.Text)
}

func (s *Service) runMessageHandler(ctx context.Context, message slacktransport.InboundMessage) {
	err := s.processMessage(ctx, message)
	if err == nil {
		return
	}

	s.recordFailure(err)
	s.logger.Error("message handling failed", "error", err)
	postErr := s.transport.PostMessage(
		context.Background(),
		message.ChannelID,
		message.ThreadTS,
		"I hit an internal error handling that message.",
	)
	if postErr != nil {
		s.logger.Error("failed to post error reply", "error", postErr)
	}
}

func (s *Service) handleReminderInput(
	ctx context.Context,
	message slacktransport.InboundMessage,
	text string,
	location *time.Location,
) (bool, error) {
	reminder, ok, reminderErr := commands.ParseReminder(s.now().In(location), location, text)
	if !ok {
		return false, nil
	}
	if reminderErr != nil {
		return true, s.transport.PostMessage(ctx, message.ChannelID, message.ThreadTS, reminderErr.Error())
	}

	response, err := s.handleReminder(ctx, message, reminder)
	if err != nil {
		return true, err
	}

	return true, s.postLocalCommand(ctx, message, text, response)
}

func (s *Service) handleCommandInput(
	ctx context.Context,
	message slacktransport.InboundMessage,
	text string,
) (bool, error) {
	command, ok := commands.Parse(text)
	if !ok {
		return false, nil
	}

	response, err := s.executeCommand(ctx, command)
	if err != nil {
		return true, err
	}

	return true, s.postLocalCommand(ctx, message, text, response)
}

func (s *Service) observeSquad0(ctx context.Context, message slacktransport.InboundMessage) (bool, error) {
	if !s.observer.Relevant(squad0.Message{
		UserID: message.UserID,
		BotID:  message.BotID,
		Text:   message.Text,
	}) {
		return false, nil
	}

	_, err := s.store.RecordEpisode(ctx, memory.EpisodeInput{
		ChannelID: message.ChannelID,
		ThreadTS:  message.ThreadTS,
		UserID:    message.UserID,
		Role:      "observer",
		Source:    "squad0",
		Text:      message.Text,
	})
	if err != nil {
		return false, err
	}

	return true, nil
}

func (s *Service) shouldRespond(message slacktransport.InboundMessage) bool {
	cfg := s.currentConfig()
	if contains(cfg.Slack.DeniedChannels, message.ChannelID) {
		return false
	}
	if len(cfg.Slack.AllowedChannels) > 0 && !contains(cfg.Slack.AllowedChannels, message.ChannelID) {
		return false
	}
	if message.IsDM {
		return cfg.Slack.AllowDM
	}
	if cfg.Slack.MentionRequiredInChannels {
		return message.Mentioned
	}

	return true
}

func (s *Service) normaliseText(text string) string {
	status := s.transport.Status()
	normalised := strings.TrimSpace(text)
	if status.BotUserID != "" {
		normalised = strings.ReplaceAll(normalised, fmt.Sprintf("<@%s>", status.BotUserID), "")
	}

	return strings.TrimSpace(normalised)
}

func (s *Service) recordFailure(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastFailure = err.Error()
	s.lastFailed = s.now().UTC()
}

func (s *Service) lastFailureText() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastFailure == "" {
		return "none"
	}

	return fmt.Sprintf("%s at %s", s.lastFailure, formatTime(s.lastFailed))
}

func (s *Service) currentConfig() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.cfg
}

func buildSearcher(cfg config.Config) web.Searcher {
	if !cfg.Web.Enabled {
		return web.NoopSearcher{}
	}

	return web.NewDuckDuckGoSearcher(cfg.Web.Timeout.Duration, cfg.Web.UserAgent)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}

	return false
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "never"
	}

	return value.Format(time.RFC3339)
}
