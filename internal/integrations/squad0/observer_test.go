package squad0

import "testing"

func TestObserverRelevantByUserID(t *testing.T) {
	t.Parallel()

	observer := New(Config{
		Enabled:         true,
		ObservedUserIDs: []string{"U123"},
	})
	if !observer.Relevant(Message{UserID: "U123"}) {
		t.Fatal("expected message to be relevant")
	}
}

func TestObserverRelevantByKeyword(t *testing.T) {
	t.Parallel()

	observer := New(Config{
		Enabled:  true,
		Keywords: []string{"squad0"},
	})
	if !observer.Relevant(Message{Text: "status update from Squad0"}) {
		t.Fatal("expected keyword match to be relevant")
	}
}

func TestObserverDisabled(t *testing.T) {
	t.Parallel()

	observer := New(Config{Enabled: false, Keywords: []string{"squad0"}})
	if observer.Relevant(Message{Text: "squad0"}) {
		t.Fatal("expected disabled observer to ignore message")
	}
}
