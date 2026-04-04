package failures

import (
	"errors"
	"testing"
)

const safeMessage = "safe"

func TestWrapAndMessage(t *testing.T) {
	t.Parallel()

	cause := errors.New("boom")
	wrapped := Wrap(cause, safeMessage)
	if wrapped == nil {
		t.Fatal("expected wrapped error")
	}
	if !errors.Is(wrapped, cause) {
		t.Fatal("wrapped error should unwrap to cause")
	}
	if got := Message(wrapped); got != safeMessage {
		t.Fatalf("Message() = %q, want %q", got, safeMessage)
	}
	if got := MessageOr(wrapped); got != safeMessage {
		t.Fatalf("MessageOr() = %q, want %q", got, safeMessage)
	}
}

func TestWrapPreservesExistingVisibleError(t *testing.T) {
	t.Parallel()

	visible := Wrap(errors.New("boom"), "first")
	wrapped := Wrap(visible, "second")
	if got := Message(wrapped); got != "first" {
		t.Fatalf("Message() = %q, want %q", got, "first")
	}
}

func TestWrapNilAndMessageFallback(t *testing.T) {
	t.Parallel()

	if got := Wrap(nil, safeMessage); got != nil {
		t.Fatal("expected nil cause to stay nil")
	}
	if got := Message(errors.New("plain")); got != "" {
		t.Fatalf("Message() = %q, want empty", got)
	}
	if got := MessageOr(errors.New("plain")); got != defaultInternalMessage {
		t.Fatalf("MessageOr() = %q, want %q", got, defaultInternalMessage)
	}
}

func TestUserVisibleErrorMethods(t *testing.T) {
	t.Parallel()

	plain := userVisibleError{message: safeMessage}
	if got := plain.Error(); got != safeMessage {
		t.Fatalf("Error() = %q, want %q", got, safeMessage)
	}
	if plain.Unwrap() != nil {
		t.Fatal("Unwrap() should be nil without a cause")
	}
	if got := plain.UserMessage(); got != safeMessage {
		t.Fatalf("UserMessage() = %q, want %q", got, safeMessage)
	}

	withCause := userVisibleError{cause: errors.New("boom"), message: safeMessage}
	if got := withCause.Error(); got != "boom" {
		t.Fatalf("Error() = %q, want %q", got, "boom")
	}
}
