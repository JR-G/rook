package commands

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	t.Parallel()

	command, ok := Parse("/status now")
	if !ok {
		t.Fatal("expected status command to parse")
	}
	if command.Kind != KindStatus {
		t.Fatalf("expected status kind, got %q", command.Kind)
	}
	if command.Args != "now" {
		t.Fatalf("expected args to be preserved, got %q", command.Args)
	}
}

func TestParseReminderRelative(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	request, ok, err := ParseReminder(now, time.UTC, "remind me in 30m to stretch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected reminder to parse")
	}
	if request.Message != "stretch" {
		t.Fatalf("unexpected reminder message %q", request.Message)
	}
	if got, want := request.DueAt, now.Add(30*time.Minute); !got.Equal(want) {
		t.Fatalf("unexpected due time %s, want %s", got, want)
	}
}

func TestParseReminderAbsolute(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	request, ok, err := ParseReminder(now, time.UTC, "remind me at 2026-04-01 11:15 to call mum")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected reminder to parse")
	}
	if request.Message != "call mum" {
		t.Fatalf("unexpected reminder message %q", request.Message)
	}
	if got := request.DueAt.Format("2006-01-02 15:04"); got != "2026-04-01 11:15" {
		t.Fatalf("unexpected due time %s", got)
	}
}

func TestParseReminderRejectsPastTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	_, ok, err := ParseReminder(now, time.UTC, "remind me at 2026-04-01 09:00 to stretch")
	if !ok {
		t.Fatal("expected reminder form to be recognised")
	}
	if err == nil {
		t.Fatal("expected past reminder to fail")
	}
}

func TestParseReminderRelativeDays(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	request, ok, err := ParseReminder(now, time.UTC, "remind me in 2d to review")
	if err != nil || !ok {
		t.Fatalf("expected day reminder to parse: ok=%t err=%v", ok, err)
	}
	if got, want := request.DueAt, now.Add(48*time.Hour); !got.Equal(want) {
		t.Fatalf("unexpected due time %s, want %s", got, want)
	}
}
