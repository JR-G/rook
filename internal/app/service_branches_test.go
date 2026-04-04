package app

import (
	"context"
	"testing"

	slacktransport "github.com/JR-G/rook/internal/slack"
)

func TestObserveSquad0AndCommandErrorBranches(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	observed, err := service.observeSquad0(context.Background(), slackMessage("hello"))
	if observed || err != nil {
		t.Fatalf("expected irrelevant message to be ignored, observed=%t err=%v", observed, err)
	}

	if err := service.processMessage(context.Background(), slacktransport.InboundMessage{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Text:      "hello",
		IsDM:      false,
		Mentioned: false,
	}); err != nil {
		t.Fatalf("expected non-mentioned channel message to be ignored: %v", err)
	}

	brokenObserver := newTestService(t)
	brokenObserver.observer = squad0ObserverForTest()
	if err := brokenObserver.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if _, err := brokenObserver.observeSquad0(context.Background(), slacktransport.InboundMessage{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		UserID:    "U-SQUAD",
		Text:      "squad0 update",
	}); err == nil {
		t.Fatal("expected observeSquad0 to fail when the store is closed")
	}

	brokenCommand := newTestService(t)
	if err := brokenCommand.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	handled, err := brokenCommand.handleCommandInput(context.Background(), slackMessage("memory"), "memory")
	if !handled || err == nil {
		t.Fatalf("expected memory command to surface a store error, handled=%t err=%v", handled, err)
	}
}
