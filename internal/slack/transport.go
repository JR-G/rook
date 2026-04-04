package slack

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// InboundMessage is the normalised message passed into the app layer.
type InboundMessage struct {
	ChannelID string
	ThreadTS  string
	UserID    string
	BotID     string
	Text      string
	IsDM      bool
	Mentioned bool
}

// Status captures runtime transport health.
type Status struct {
	Connected   bool
	BotUserID   string
	BotID       string
	LastEventAt time.Time
	LastError   string
}

// Transport wraps Slack Socket Mode.
type Transport struct {
	api    slackAPI
	client socketClient
	events <-chan socketmode.Event
	logger *slog.Logger

	mu     sync.RWMutex
	status Status
}

type slackAPI interface {
	AuthTestContext(context.Context) (*slack.AuthTestResponse, error)
	PostMessageContext(context.Context, string, ...slack.MsgOption) (string, string, error)
}

type socketClient interface {
	RunContext(context.Context) error
	Ack(socketmode.Request, ...interface{})
}

// New creates a Slack transport.
func New(botToken, appToken string, logger *slog.Logger) *Transport {
	api := slack.New(
		botToken,
		slack.OptionAppLevelToken(appToken),
	)
	client := socketmode.New(api)

	return newTransport(api, client, client.Events, logger)
}

func newTransport(api slackAPI, client socketClient, events <-chan socketmode.Event, logger *slog.Logger) *Transport {
	return &Transport{
		api:    api,
		client: client,
		events: events,
		logger: logger,
	}
}

// Run starts the Socket Mode event loop.
func (t *Transport) Run(ctx context.Context, handler func(context.Context, InboundMessage)) error {
	auth, err := t.api.AuthTestContext(ctx)
	if err != nil {
		return err
	}
	t.updateStatus(func(status *Status) {
		status.BotUserID = auth.UserID
		status.BotID = auth.BotID
	})

	go func() {
		if runErr := t.client.RunContext(ctx); runErr != nil {
			t.setError(runErr)
			t.logger.Error("slack socket mode stopped", "error", runErr)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-t.events:
			t.handleEvent(ctx, event, handler)
		}
	}
}

// PostMessage sends a Slack message or threaded reply.
func (t *Transport) PostMessage(ctx context.Context, channelID, threadTS, text string) error {
	options := []slack.MsgOption{
		slack.MsgOptionText(text, false),
	}
	if threadTS != "" {
		options = append(options, slack.MsgOptionPostMessageParameters(slack.PostMessageParameters{
			ThreadTimestamp: threadTS,
		}))
	}

	_, _, err := t.api.PostMessageContext(ctx, channelID, options...)

	return err
}

// Status returns the current transport snapshot.
func (t *Transport) Status() Status {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.status
}

func (t *Transport) handleEvent(ctx context.Context, event socketmode.Event, handler func(context.Context, InboundMessage)) {
	switch event.Type {
	case socketmode.EventTypeConnecting:
		t.updateStatus(func(status *Status) {
			status.Connected = false
			status.LastEventAt = time.Now().UTC()
		})
	case socketmode.EventTypeConnected:
		t.updateStatus(func(status *Status) {
			status.Connected = true
			status.LastEventAt = time.Now().UTC()
			status.LastError = ""
		})
	case socketmode.EventTypeConnectionError:
		t.setError(fmt.Errorf("slack connection error"))
	case socketmode.EventTypeInvalidAuth,
		socketmode.EventTypeIncomingError,
		socketmode.EventTypeErrorWriteFailed,
		socketmode.EventTypeErrorBadMessage,
		socketmode.EventTypeDisconnect:
		t.setError(fmt.Errorf("slack socket event: %s", event.Type))
	case socketmode.EventTypeHello,
		socketmode.EventTypeInteractive,
		socketmode.EventTypeSlashCommand:
		if event.Request != nil {
			t.client.Ack(*event.Request)
		}

		t.updateStatus(func(status *Status) {
			status.LastEventAt = time.Now().UTC()
		})
	case socketmode.EventTypeEventsAPI:
		if event.Request != nil {
			t.client.Ack(*event.Request)
		}

		apiEvent, ok := event.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}

		t.updateStatus(func(status *Status) {
			status.LastEventAt = time.Now().UTC()
		})
		t.handleEventsAPI(ctx, apiEvent, handler)
	}
}

func (t *Transport) handleEventsAPI(ctx context.Context, apiEvent slackevents.EventsAPIEvent, handler func(context.Context, InboundMessage)) {
	switch inner := apiEvent.InnerEvent.Data.(type) {
	case *slackevents.AppMentionEvent:
		handler(ctx, InboundMessage{
			ChannelID: inner.Channel,
			ThreadTS:  defaultThread(inner.ThreadTimeStamp, inner.TimeStamp),
			UserID:    inner.User,
			Text:      inner.Text,
			IsDM:      false,
			Mentioned: true,
		})
	case *slackevents.MessageEvent:
		if inner.SubType != "" && inner.SubType != "bot_message" {
			return
		}

		status := t.Status()
		if inner.User == status.BotUserID || inner.BotID == status.BotID {
			return
		}

		handler(ctx, InboundMessage{
			ChannelID: inner.Channel,
			ThreadTS:  defaultThread(inner.ThreadTimeStamp, inner.TimeStamp),
			UserID:    inner.User,
			BotID:     inner.BotID,
			Text:      inner.Text,
			IsDM:      inner.ChannelType == "im",
			Mentioned: status.BotUserID != "" && strings.Contains(inner.Text, fmt.Sprintf("<@%s>", status.BotUserID)),
		})
	}
}

func (t *Transport) updateStatus(update func(*Status)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	update(&t.status)
}

func (t *Transport) setError(err error) {
	t.updateStatus(func(status *Status) {
		status.LastError = err.Error()
		status.LastEventAt = time.Now().UTC()
		status.Connected = false
	})
}

func defaultThread(threadTS, eventTS string) string {
	if threadTS != "" {
		return threadTS
	}

	return eventTS
}
