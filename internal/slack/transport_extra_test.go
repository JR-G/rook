package slack

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func TestNewAndRunAuthFailure(t *testing.T) {
	t.Parallel()

	if transport := New("bot-token", "app-token", slog.New(slog.NewTextHandler(io.Discard, nil))); transport == nil {
		t.Fatal("expected constructor to return transport")
	}

	transport := newTransport(&fakeAPI{authErr: errors.New("auth failed")}, &fakeSocket{}, make(chan socketmode.Event), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := transport.Run(context.Background(), func(context.Context, InboundMessage) {}); err == nil {
		t.Fatal("expected auth failure")
	}
}

func TestHandleEventStatusVariants(t *testing.T) {
	t.Parallel()

	socket := &fakeSocket{}
	transport := newTransport(&fakeAPI{}, socket, make(chan socketmode.Event), slog.New(slog.NewTextHandler(io.Discard, nil)))
	handler := func(context.Context, InboundMessage) {}

	transport.handleEvent(context.Background(), socketmode.Event{Type: socketmode.EventTypeConnecting}, handler)
	if transport.Status().Connected {
		t.Fatal("expected connecting to clear connected state")
	}

	transport.handleEvent(context.Background(), socketmode.Event{Type: socketmode.EventTypeConnected}, handler)
	if !transport.Status().Connected {
		t.Fatal("expected connected event to set status")
	}

	errorEvents := []socketmode.EventType{
		socketmode.EventTypeConnectionError,
		socketmode.EventTypeInvalidAuth,
		socketmode.EventTypeIncomingError,
		socketmode.EventTypeErrorWriteFailed,
		socketmode.EventTypeErrorBadMessage,
		socketmode.EventTypeDisconnect,
	}
	for _, eventType := range errorEvents {
		transport.handleEvent(context.Background(), socketmode.Event{Type: eventType}, handler)
		if transport.Status().LastError == "" {
			t.Fatalf("expected error to be recorded for %s", eventType)
		}
	}

	request := socketmode.Request{}
	ackEvents := []socketmode.EventType{
		socketmode.EventTypeHello,
		socketmode.EventTypeInteractive,
		socketmode.EventTypeSlashCommand,
	}
	for _, eventType := range ackEvents {
		transport.handleEvent(context.Background(), socketmode.Event{Type: eventType, Request: &request}, handler)
	}
	if socket.acked != len(ackEvents) {
		t.Fatalf("expected ack count %d, got %d", len(ackEvents), socket.acked)
	}

	transport.handleEvent(context.Background(), socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: "not an api event",
	}, handler)
}

func TestHandleEventsAPIBranches(t *testing.T) {
	t.Parallel()

	transport := &Transport{
		status: Status{BotUserID: "UROOK", BotID: "BROOK"},
	}

	transport.handleEventsAPI(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Data: &slackevents.MessageEvent{
				User:    "U1",
				BotID:   "OTHER",
				Text:    "ignored",
				SubType: "message_changed",
			},
		},
	}, func(context.Context, InboundMessage) {
		t.Fatal("did not expect changed message event to be handled")
	})

	transport.handleEventsAPI(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Data: &slackevents.MessageEvent{
				User:  "U2",
				BotID: "BROOK",
				Text:  "ignored bot message",
			},
		},
	}, func(context.Context, InboundMessage) {
		t.Fatal("did not expect own bot message to be handled")
	})

	handled := make(chan InboundMessage, 1)
	transport.handleEventsAPI(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Data: &slackevents.MessageEvent{
				Channel:         "D1",
				ChannelType:     "im",
				ThreadTimeStamp: "",
				TimeStamp:       "1.1",
				User:            "U2",
				Text:            "<@UROOK> hello",
			},
		},
	}, func(_ context.Context, message InboundMessage) {
		handled <- message
	})

	select {
	case message := <-handled:
		if !message.IsDM || !message.Mentioned || message.ThreadTS != "1.1" {
			t.Fatalf("unexpected handled message %#v", message)
		}
	default:
		t.Fatal("expected direct message event to be handled")
	}
}
