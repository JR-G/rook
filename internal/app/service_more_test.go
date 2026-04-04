package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JR-G/rook/internal/commands"
	"github.com/JR-G/rook/internal/memory"
	slacktransport "github.com/JR-G/rook/internal/slack"
)

type errorTransport struct {
	fakeTransport
	postErr error
}

func (e *errorTransport) PostMessage(context.Context, string, string, string) error {
	return e.postErr
}

func TestShouldRespondBranchesAndRunFailure(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	service.cfg.Slack.AllowDM = false
	if shouldRespond, err := service.shouldRespond(context.Background(), slackMessage("hello")); err != nil || shouldRespond {
		t.Fatal("did not expect DMs to be allowed")
	}

	service.cfg.Slack.AllowDM = true
	if shouldRespond, err := service.shouldRespond(context.Background(), slackMessage("hello")); err != nil || !shouldRespond {
		t.Fatal("expected DM to be allowed")
	}

	channelMessage := slackMessage("hello")
	channelMessage.IsDM = false
	channelMessage.Mentioned = false
	if shouldRespond, err := service.shouldRespond(context.Background(), channelMessage); err != nil || shouldRespond {
		t.Fatal("did not expect unmentioned channel message to be allowed")
	}

	channelMessage.Mentioned = true
	service.cfg.Slack.AllowedChannels = []string{"C1"}
	channelMessage.ChannelID = "C2"
	if shouldRespond, err := service.shouldRespond(context.Background(), channelMessage); err != nil || shouldRespond {
		t.Fatal("did not expect disallowed channel to be allowed")
	}

	service.cfg.Slack.AllowedChannels = nil
	service.cfg.Slack.DeniedChannels = []string{"C2"}
	if shouldRespond, err := service.shouldRespond(context.Background(), channelMessage); err != nil || shouldRespond {
		t.Fatal("did not expect denied channel to be allowed")
	}

	transport := requireFakeTransport(t, service)
	transport.runErr = errors.New("run failed")
	if err := service.Run(context.Background()); err == nil || !strings.Contains(service.lastFailureText(), "run failed") {
		t.Fatalf("expected run failure to be recorded, got %v / %q", err, service.lastFailureText())
	}
}

func TestShouldRespondAllowsActiveThreadContinuation(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	if _, err := service.store.RecordEpisode(context.Background(), memory.EpisodeInput{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		UserID:    "rook",
		Role:      "assistant",
		Source:    "assistant",
		Text:      "Earlier reply",
	}); err != nil {
		t.Fatalf("record assistant episode: %v", err)
	}

	channelMessage := slackMessage("hello again")
	channelMessage.ChannelID = "C1"
	channelMessage.IsDM = false
	channelMessage.Mentioned = false

	shouldRespond, err := service.shouldRespond(context.Background(), channelMessage)
	if err != nil {
		t.Fatalf("shouldRespond() error = %v", err)
	}
	if !shouldRespond {
		t.Fatal("expected active thread continuation to be allowed")
	}
}

func TestHandleReminderPostLocalCommandAndDispatchFailures(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	service.now = func() time.Time { return time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC) }
	response, err := service.handleReminder(context.Background(), slackMessage("remind"), commands.ReminderRequest{
		DueAt:   service.now().Add(time.Minute),
		Message: "stretch",
	})
	if err != nil || !strings.Contains(response, "Reminder set") {
		t.Fatalf("unexpected reminder response %q err=%v", response, err)
	}

	if err := service.postLocalCommand(context.Background(), slackMessage("ping"), "ping", "pong"); err != nil {
		t.Fatalf("post local command: %v", err)
	}

	failingService := newTestService(t)
	failingService.transport = &errorTransport{postErr: errors.New("post failed")}
	if err := failingService.postLocalCommand(context.Background(), slackMessage("ping"), "ping", "pong"); err == nil {
		t.Fatal("expected postLocalCommand to fail when transport post fails")
	}

	if _, err := service.store.AddReminder(context.Background(), memory.ReminderInput{
		ChannelID: "D1",
		ThreadTS:  "1.0",
		Message:   "stretch",
		DueAt:     service.now().Add(-time.Minute),
		CreatedBy: "U1",
	}); err != nil {
		t.Fatalf("add due reminder: %v", err)
	}
	failingDispatch := newTestService(t)
	failingDispatch.transport = &errorTransport{postErr: errors.New("post failed")}
	if _, err := failingDispatch.store.AddReminder(context.Background(), memory.ReminderInput{
		ChannelID: "D1",
		ThreadTS:  "1.0",
		Message:   "stretch",
		DueAt:     failingDispatch.now().Add(-time.Minute),
		CreatedBy: "U1",
	}); err != nil {
		t.Fatalf("add due reminder: %v", err)
	}
	if err := failingDispatch.dispatchDueReminders(context.Background()); err == nil {
		t.Fatal("expected dispatch due reminders to fail when post fails")
	}
}

func TestProcessMessageNoReplyAndReloadFailure(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	service.transport = &fakeTransport{status: slacktransport.Status{BotUserID: "UROOK"}}
	if err := service.processMessage(context.Background(), slacktransport.InboundMessage{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Text:      "<@UROOK>",
		IsDM:      false,
		Mentioned: true,
	}); err != nil {
		t.Fatalf("expected empty normalised message to be ignored: %v", err)
	}

	service.cfg.Service.Timezone = "Bad/Timezone"
	if err := service.processMessage(context.Background(), slackMessage("hello")); err == nil {
		t.Fatal("expected invalid timezone to fail")
	}

	service.cfgPath = "missing.toml"
	if reply := service.reload(); !strings.Contains(reply, "reload failed") {
		t.Fatalf("unexpected reload reply %q", reply)
	}
	if reply, err := service.executeCommand(context.Background(), commands.Command{Kind: commands.KindModel, Args: "set phi4-mini"}); err != nil || !strings.Contains(reply, "phi4-mini") {
		t.Fatalf("unexpected model command response %q err=%v", reply, err)
	}
	if reply, err := service.executeCommand(context.Background(), commands.Command{Kind: commands.KindReload}); err != nil || !strings.Contains(reply, "reload failed") {
		t.Fatalf("unexpected reload command response %q err=%v", reply, err)
	}
}
