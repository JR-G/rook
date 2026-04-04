package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreUpsertSearchReminderAndPersona(t *testing.T) {
	t.Parallel()

	currentTime := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	store := newTestStore(t, func() time.Time { return currentTime })
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	item, err := store.UpsertMemory(ctx, Candidate{
		Type:       Preference,
		Scope:      ScopeUser,
		Subject:    "short replies",
		Body:       "Prefer short replies.",
		Keywords:   []string{"short", "replies"},
		Confidence: 0.9,
		Importance: 0.8,
		Embedding:  []float64{1, 0},
		Source:     "user",
	})
	if err != nil {
		t.Fatalf("upsert insert failed: %v", err)
	}

	if _, err := store.UpsertMemory(ctx, Candidate{
		Type:       Preference,
		Scope:      ScopeUser,
		Subject:    "short replies",
		Body:       "Prefer short replies in Slack.",
		Keywords:   []string{"short", "slack"},
		Confidence: 0.95,
		Importance: 0.85,
		Embedding:  []float64{1, 0},
		Source:     "user",
	}); err != nil {
		t.Fatalf("upsert merge failed: %v", err)
	}

	hits, err := store.SearchMemories(ctx, "short replies", []float64{1, 0}, 5)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(hits) == 0 || hits[0].Item.ID != item.ID {
		t.Fatalf("expected stored item in search hits: %#v", hits)
	}

	if _, err := store.RecordEpisode(ctx, EpisodeInput{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Role:      "user",
		Source:    "user",
		Text:      "hello world",
	}); err != nil {
		t.Fatalf("record episode failed: %v", err)
	}

	reminder, err := store.AddReminder(ctx, ReminderInput{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		Message:   "stretch",
		DueAt:     currentTime.Add(time.Minute),
		CreatedBy: "U1",
	})
	if err != nil {
		t.Fatalf("add reminder failed: %v", err)
	}

	due, err := store.DueReminders(ctx, currentTime.Add(2*time.Minute), 5)
	if err != nil {
		t.Fatalf("due reminders failed: %v", err)
	}
	if len(due) != 1 || due[0].ID != reminder.ID {
		t.Fatalf("expected due reminder, got %#v", due)
	}

	if err := store.MarkReminderSent(ctx, reminder.ID, currentTime.Add(2*time.Minute)); err != nil {
		t.Fatalf("mark reminder sent failed: %v", err)
	}

	if err := store.EnsurePersonaLayer(ctx, "stable_identity", "seed", "test"); err != nil {
		t.Fatalf("ensure persona layer failed: %v", err)
	}
	if _, err := store.UpdatePersonaLayer(ctx, "stable_identity", "updated", "reason", "test"); err != nil {
		t.Fatalf("update persona layer failed: %v", err)
	}

	health, err := store.Health(ctx)
	if err != nil {
		t.Fatalf("health failed: %v", err)
	}
	if !health.Reachable {
		t.Fatal("expected store to be healthy")
	}
}

func TestSearchContextSeparatesBuckets(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, time.Now)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	for _, candidate := range []Candidate{
		{
			Type:       Fact,
			Scope:      ScopeUser,
			Subject:    "name",
			Body:       "Preferred name is Rook User",
			Keywords:   []string{"name"},
			Confidence: 0.9,
			Importance: 0.9,
		},
		{
			Type:       Project,
			Scope:      ScopeUser,
			Subject:    "rook",
			Body:       "Working on rook",
			Keywords:   []string{"rook"},
			Confidence: 0.8,
			Importance: 0.8,
		},
	} {
		if _, err := store.UpsertMemory(ctx, candidate); err != nil {
			t.Fatalf("upsert failed: %v", err)
		}
	}

	contextResult, err := store.SearchContext(ctx, "rook name", nil, RetrievalLimits{
		MaxPromptItems:  4,
		MaxEpisodeItems: 2,
	})
	if err != nil {
		t.Fatalf("search context failed: %v", err)
	}
	if len(contextResult.UserFacts) == 0 {
		t.Fatal("expected user facts")
	}
	if len(contextResult.WorkingContext) == 0 {
		t.Fatal("expected working context")
	}
}

func TestStoreMaintenanceHelpers(t *testing.T) {
	t.Parallel()

	currentTime := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	store := newTestStore(t, func() time.Time { return currentTime })
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	if _, err := store.UpsertMemory(ctx, Candidate{
		Type:       Preference,
		Scope:      ScopeUser,
		Subject:    "stale item",
		Body:       "stale item",
		Confidence: 0.4,
		Importance: 0.2,
		Source:     "user",
	}); err != nil {
		t.Fatalf("upsert stale memory: %v", err)
	}
	if _, err := store.RecordEpisode(ctx, EpisodeInput{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Role:      "user",
		Source:    "user",
		Text:      "old episode",
	}); err != nil {
		t.Fatalf("record episode: %v", err)
	}

	if err := store.Decay(ctx); err != nil {
		t.Fatalf("decay: %v", err)
	}
	if err := store.PruneEpisodes(ctx, 1); err != nil {
		t.Fatalf("prune episodes: %v", err)
	}

	if _, err := store.ListRecentMemories(ctx, 5); err != nil {
		t.Fatalf("list recent memories: %v", err)
	}
	if _, err := store.MemoriesByTypes(ctx, []Type{Preference}, 0, 5); err != nil {
		t.Fatalf("memories by type: %v", err)
	}
	if _, err := store.RecentEpisodes(ctx, 5); err != nil {
		t.Fatalf("recent episodes: %v", err)
	}
	if _, err := store.PendingReminderCount(ctx); err != nil {
		t.Fatalf("pending reminder count: %v", err)
	}
	if store.String() == "" {
		t.Fatal("expected store string")
	}
}

func newTestStore(t *testing.T, clock func() time.Time) *Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "rook.sqlite")
	store, err := OpenWithClock(path, clock)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	return store
}
