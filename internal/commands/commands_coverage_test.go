package commands

import (
	"strings"
	"testing"
	"time"
)

func TestParseSlashOnlyAndReminderFutureChecks(t *testing.T) {
	t.Parallel()

	if _, ok := Parse("/"); ok {
		t.Fatal("expected slash-only input to be ignored")
	}

	duration, err := parseRelativeDuration("2d")
	if err != nil || duration != 48*time.Hour {
		t.Fatalf("unexpected day duration %s err=%v", duration, err)
	}

	now := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	_, ok, err := ParseReminder(now, time.UTC, "remind me in 0m to stretch")
	if !ok || err == nil || !strings.Contains(err.Error(), "future") {
		t.Fatalf("expected future-time validation error, got ok=%t err=%v", ok, err)
	}

	if _, err := parseRelativeDuration("bd"); err == nil {
		t.Fatal("expected invalid day duration to fail")
	}
}
