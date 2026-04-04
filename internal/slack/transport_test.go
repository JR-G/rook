package slack

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type fakeAPI struct {
	authResponse *slack.AuthTestResponse
	authErr      error
	postCalls    int
	lastChannel  string
	lastOptions  int
}

func (f *fakeAPI) AuthTestContext(context.Context) (*slack.AuthTestResponse, error) {
	return f.authResponse, f.authErr
}

func (f *fakeAPI) PostMessageContext(_ context.Context, channel string, options ...slack.MsgOption) (channelID, threadTS string, err error) {
	f.postCalls++
	f.lastChannel = channel
	f.lastOptions = len(options)

	return channel, "1.0", nil
}

type fakeSocket struct {
	acked  int
	runErr error
}

func (f *fakeSocket) RunContext(ctx context.Context) error {
	if f.runErr != nil {
		return f.runErr
	}
	<-ctx.Done()

	return ctx.Err()
}

func (f *fakeSocket) Ack(socketmode.Request, ...interface{}) {
	f.acked++
}

func TestRunHandlesConnectedAndMentionEvents(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{
		authResponse: &slack.AuthTestResponse{
			UserID: "UROOK",
			BotID:  "BROOK",
		},
	}
	socket := &fakeSocket{}
	events := make(chan socketmode.Event, 4)
	transport := newTransport(api, socket, events, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handled := make(chan InboundMessage, 1)
	go func() {
		_ = transport.Run(ctx, func(_ context.Context, message InboundMessage) {
			handled <- message
		})
	}()

	events <- socketmode.Event{Type: socketmode.EventTypeConnected}
	events <- socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Data: &slackevents.AppMentionEvent{
					Channel:         "C1",
					ThreadTimeStamp: "1.0",
					TimeStamp:       "1.1",
					User:            "U1",
					Text:            "<@UROOK> ping",
				},
			},
		},
	}

	select {
	case message := <-handled:
		if message.ChannelID != "C1" || !message.Mentioned {
			t.Fatalf("unexpected inbound message %#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handled message")
	}

	status := transport.Status()
	if !status.Connected || status.BotUserID != "UROOK" || status.BotID != "BROOK" {
		t.Fatalf("unexpected status %#v", status)
	}
}

func TestPostMessageAndErrorStatus(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{
		authResponse: &slack.AuthTestResponse{UserID: "UROOK"},
	}
	socket := &fakeSocket{}
	transport := newTransport(api, socket, make(chan socketmode.Event), slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := transport.PostMessage(context.Background(), "C1", "1.0", "hello"); err != nil {
		t.Fatalf("post message failed: %v", err)
	}
	if api.postCalls != 1 || api.lastChannel != "C1" || api.lastOptions == 0 {
		t.Fatalf("unexpected post call state %#v", api)
	}

	transport.handleEvent(context.Background(), socketmode.Event{Type: socketmode.EventTypeConnectionError}, func(context.Context, InboundMessage) {})
	if transport.Status().LastError == "" {
		t.Fatal("expected connection error to update status")
	}
}

func TestHandleEventsAPIIgnoresOwnMessages(t *testing.T) {
	t.Parallel()

	transport := &Transport{
		status: Status{BotUserID: "UROOK", BotID: "BROOK"},
	}
	called := false
	transport.handleEventsAPI(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Data: &slackevents.MessageEvent{
				User: "UROOK",
				Text: "hello",
			},
		},
	}, func(_ context.Context, _ InboundMessage) {
		called = true
	})

	if called {
		t.Fatal("expected own message to be ignored")
	}
	if defaultThread("", "1.1") != "1.1" {
		t.Fatal("expected default thread fallback")
	}
}
