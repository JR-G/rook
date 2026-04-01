package logging

import "testing"

func TestNewAcceptsKnownLevels(t *testing.T) {
	t.Parallel()

	for _, level := range []string{"debug", "info", "warn", "error"} {
		logger, err := New(level)
		if err != nil {
			t.Fatalf("level %q returned error: %v", level, err)
		}
		if logger == nil {
			t.Fatalf("level %q returned nil logger", level)
		}
	}
}

func TestNewRejectsUnknownLevel(t *testing.T) {
	t.Parallel()

	if _, err := New("trace"); err == nil {
		t.Fatal("expected invalid log level to fail")
	}
}
