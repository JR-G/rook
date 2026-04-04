package commands

import (
	"testing"
	"time"
)

func TestParseVariantsAndUnknownInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		kind  Kind
		args  string
		ok    bool
	}{
		{input: "/help", kind: KindHelp, ok: true},
		{input: "ping", kind: KindPing, ok: true},
		{input: "memory name", kind: KindMemory, args: "name", ok: true},
		{input: "model set qwen3:4b", kind: KindModel, args: "set qwen3:4b", ok: true},
		{input: "", ok: false},
		{input: "hello there", ok: false},
	}

	for _, testCase := range cases {
		command, ok := Parse(testCase.input)
		if ok != testCase.ok {
			t.Fatalf("unexpected parse state for %q: ok=%t", testCase.input, ok)
		}
		if !ok {
			continue
		}
		if command.Kind != testCase.kind || command.Args != testCase.args {
			t.Fatalf("unexpected command %#v for %q", command, testCase.input)
		}
	}
}

func TestParseReminderErrorsAndHelpers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	if _, ok, err := ParseReminder(now, time.UTC, "hello"); ok || err != nil {
		t.Fatalf("expected non-reminder input to be ignored: ok=%t err=%v", ok, err)
	}

	if _, ok, err := ParseReminder(now, time.UTC, "remind me in nope to stretch"); !ok || err == nil {
		t.Fatalf("expected invalid relative reminder to fail: ok=%t err=%v", ok, err)
	}
	if _, ok, err := ParseReminder(now, time.UTC, "remind me at no-time to stretch"); !ok || err == nil {
		t.Fatalf("expected invalid absolute reminder to fail: ok=%t err=%v", ok, err)
	}

	if got, err := parseRelativeDuration("45m"); err != nil || got != 45*time.Minute {
		t.Fatalf("unexpected relative duration %s err=%v", got, err)
	}
	if _, err := parseRelativeDuration("4x"); err == nil {
		t.Fatal("expected invalid relative duration to fail")
	}

	dueAt, err := parseAbsoluteTime(time.UTC, "2026-04-01T12:30:00Z")
	if err != nil || dueAt.UTC().Hour() != 12 {
		t.Fatalf("unexpected RFC3339 parse result %s err=%v", dueAt, err)
	}

	inLocation, err := parseAtLayout(time.UTC, "2006-01-02 15:04", "2026-04-01 13:00")
	if err != nil || inLocation.Hour() != 13 {
		t.Fatalf("unexpected location parse result %s err=%v", inLocation, err)
	}
}
