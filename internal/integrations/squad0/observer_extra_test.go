package squad0

import "testing"

func TestObserverRelevantByBotAndNonMatch(t *testing.T) {
	t.Parallel()

	observer := New(Config{
		Enabled:        true,
		ObservedBotIDs: []string{"B123"},
		Keywords:       []string{"squad0"},
	})
	if !observer.Relevant(Message{BotID: "B123"}) {
		t.Fatal("expected observed bot message to be relevant")
	}
	if observer.Relevant(Message{UserID: "U999", Text: "plain update"}) {
		t.Fatal("did not expect unrelated message to be relevant")
	}
}
