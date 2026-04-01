package slack

import (
	"context"
	"testing"

	"github.com/slack-go/slack/slackevents"
)

func TestHandleEventsAPIAppMention(t *testing.T) {
	t.Parallel()

	transport := &Transport{}
	var got InboundMessage
	transport.handleEventsAPI(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Data: &slackevents.AppMentionEvent{
				Channel:         "C1",
				ThreadTimeStamp: "1.0",
				TimeStamp:       "1.1",
				User:            "U1",
				Text:            "<@UROOK> ping",
			},
		},
	}, func(_ context.Context, message InboundMessage) {
		got = message
	})

	if got.ChannelID != "C1" || !got.Mentioned {
		t.Fatalf("unexpected inbound message %#v", got)
	}
}

func TestHandleEventsAPIMessageIgnoresOwnMessages(t *testing.T) {
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
}
