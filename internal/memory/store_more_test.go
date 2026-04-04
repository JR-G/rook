package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenWithClockFailureAndHealthBranches(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	parentFile := filepath.Join(tempDir, "not-a-dir")
	if err := os.WriteFile(parentFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("write parent file: %v", err)
	}
	if _, err := OpenWithClock(filepath.Join(parentFile, "rook.sqlite"), time.Now); err == nil {
		t.Fatal("expected mkdir failure for invalid db parent")
	}

	now := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	store := newTestStore(t, func() time.Time { return now })
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	if _, err := store.UpsertMemory(ctx, Candidate{
		Type:       Fact,
		Scope:      ScopeUser,
		Subject:    "name",
		Body:       "James",
		Confidence: 0.9,
		Importance: 0.8,
	}); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}
	if _, err := store.RecordEpisode(ctx, EpisodeInput{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Role:      "user",
		Source:    "user",
		Text:      "hello",
	}); err != nil {
		t.Fatalf("record episode: %v", err)
	}
	if _, err := store.AddReminder(ctx, ReminderInput{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		Message:   "stretch",
		DueAt:     now.Add(time.Minute),
		CreatedBy: "U1",
	}); err != nil {
		t.Fatalf("add reminder: %v", err)
	}

	health, err := store.Health(ctx)
	if err != nil || health.MemoryCount != 1 || health.EpisodeCount != 1 || health.PendingReminds != 1 {
		t.Fatalf("unexpected health %#v err=%v", health, err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if _, err := store.Health(ctx); err == nil {
		t.Fatal("expected health on closed store to fail")
	}
}

func TestSearchContextAndReminderBranches(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	store := newTestStore(t, func() time.Time { return now })
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	candidates := []Candidate{
		{Type: Fact, Scope: ScopeUser, Subject: "name", Body: "James", Confidence: 0.99, Importance: 0.9},
		{Type: RelationshipNote, Scope: ScopeUser, Subject: "partner", Body: "Partner is Alex", Confidence: 0.95, Importance: 0.8},
		{Type: Project, Scope: ScopeUser, Subject: "rook", Body: "Working on rook", Confidence: 0.9, Importance: 0.85},
		{Type: Decision, Scope: ScopeUser, Subject: "launch", Body: "Ship rook in April", Confidence: 0.97, Importance: 0.95},
	}
	for _, candidate := range candidates {
		if _, err := store.UpsertMemory(ctx, candidate); err != nil {
			t.Fatalf("upsert memory: %v", err)
		}
	}

	for _, episode := range []EpisodeInput{
		{ChannelID: "C1", ThreadTS: "1.0", UserID: "U1", Role: "user", Source: "user", Text: "rook launch plan"},
		{ChannelID: "C1", ThreadTS: "1.0", UserID: "U2", Role: "observer", Source: "squad0", Text: "rook launch update"},
	} {
		if _, err := store.RecordEpisode(ctx, episode); err != nil {
			t.Fatalf("record episode: %v", err)
		}
	}

	contextResult, err := store.SearchContext(ctx, "rook launch", []float64{1, 0}, RetrievalLimits{
		MaxPromptItems:  4,
		MaxEpisodeItems: 1,
	})
	if err != nil {
		t.Fatalf("search context: %v", err)
	}
	if len(contextResult.UserFacts) == 0 || len(contextResult.WorkingContext) == 0 {
		t.Fatalf("unexpected retrieval context %#v", contextResult)
	}
	if len(contextResult.Episodes) != 1 || len(contextResult.Squad0Episodes) != 1 {
		t.Fatalf("unexpected episode buckets %#v", contextResult)
	}
	if hasReply, err := store.HasAssistantReplyInThread(ctx, "C1", "1.0"); err != nil || hasReply {
		t.Fatalf("unexpected assistant thread state before reply: %t err=%v", hasReply, err)
	}
	if _, err := store.RecordEpisode(ctx, EpisodeInput{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		UserID:    "rook",
		Role:      "assistant",
		Source:    "assistant",
		Text:      "Reply",
	}); err != nil {
		t.Fatalf("record assistant episode: %v", err)
	}
	if hasReply, err := store.HasAssistantReplyInThread(ctx, "C1", "1.0"); err != nil || !hasReply {
		t.Fatalf("unexpected assistant thread state after reply: %t err=%v", hasReply, err)
	}
	if hasReply, err := store.HasAssistantReplyInThread(ctx, "C1", "2.0"); err != nil || hasReply {
		t.Fatalf("unexpected assistant thread state for other thread: %t err=%v", hasReply, err)
	}

	filtered, err := store.MemoriesByTypes(ctx, []Type{Fact, Project, Decision}, 0.95, 1)
	if err != nil || len(filtered) != 1 || filtered[0].Type != Decision {
		t.Fatalf("unexpected filtered memories %#v err=%v", filtered, err)
	}
}

func TestInsertMemoryRecordEpisodeAndReminderCounts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	store := newTestStore(t, func() time.Time { return now })
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	item, err := store.insertMemory(ctx, Candidate{
		Type:       Preference,
		Scope:      ScopeUser,
		Subject:    "tone",
		Body:       "Prefer concise replies.",
		Keywords:   []string{"tone"},
		Confidence: 0.9,
		Importance: 0.8,
		Source:     "user",
	}, "tone", `["tone"]`, "", now)
	if err != nil {
		t.Fatalf("insert memory: %v", err)
	}
	var eventCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_events WHERE memory_id = ?`, item.ID).Scan(&eventCount); err != nil {
		t.Fatalf("query memory events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("unexpected memory event count %d", eventCount)
	}

	episode, err := store.RecordEpisode(ctx, EpisodeInput{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Role:      "user",
		Source:    "user",
		Text:      strings.Repeat("a", 2600),
	})
	if err != nil {
		t.Fatalf("record episode: %v", err)
	}
	if !strings.HasSuffix(episode.Text, "…") || !strings.HasSuffix(episode.Summary, "…") {
		t.Fatalf("unexpected episode truncation %#v", episode)
	}

	reminder, err := store.AddReminder(ctx, ReminderInput{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		Message:   "stretch",
		DueAt:     now.Add(time.Minute),
		CreatedBy: "U1",
	})
	if err != nil {
		t.Fatalf("add reminder: %v", err)
	}
	if count, err := store.PendingReminderCount(ctx); err != nil || count != 1 {
		t.Fatalf("unexpected pending reminder count %d err=%v", count, err)
	}
	if due, err := store.DueReminders(ctx, now.Add(2*time.Minute), 1); err != nil || len(due) != 1 || due[0].ID != reminder.ID {
		t.Fatalf("unexpected due reminders %#v err=%v", due, err)
	}
	if err := store.MarkReminderSent(ctx, reminder.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("mark reminder sent: %v", err)
	}
	if count, err := store.PendingReminderCount(ctx); err != nil || count != 0 {
		t.Fatalf("unexpected pending reminder count after send %d err=%v", count, err)
	}
}

func TestTokenizeAndTopNItemBranches(t *testing.T) {
	t.Parallel()

	if tokens := tokenize("!!!"); tokens != nil {
		t.Fatalf("expected punctuation-only tokenize to return nil, got %#v", tokens)
	}
	if items := topNItems([]int{1, 2, 3}, 0, func(value int) float64 { return float64(value) }); items != nil {
		t.Fatalf("expected zero-limit topNItems to return nil, got %#v", items)
	}
	items := topNItems([]int{1, 2, 3}, 2, func(value int) float64 { return float64(value) })
	if len(items) != 2 || items[0] != 3 || items[1] != 2 {
		t.Fatalf("unexpected top items %#v", items)
	}
}
