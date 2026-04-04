package memory

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreOpenCloseAndValidationHelpers(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "rook.sqlite"))
	if err != nil {
		t.Fatalf("open with real clock: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	var nilStore *Store
	if err := nilStore.Close(); err != nil {
		t.Fatalf("close nil store: %v", err)
	}

	reopened := newTestStore(t, time.Now)
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err := reopened.UpsertMemory(context.Background(), Candidate{}); err == nil {
		t.Fatal("expected invalid memory candidate to fail")
	}
	if err := reopened.PruneEpisodes(context.Background(), 0); err != nil {
		t.Fatalf("expected zero retention to be a no-op: %v", err)
	}
}

func TestStoreMergeAndQueryBranches(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, time.Now)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	item, err := store.UpsertMemory(ctx, Candidate{
		Type:       Preference,
		Scope:      ScopeUser,
		Subject:    "tone",
		Body:       "Prefer concise replies.",
		Keywords:   []string{"concise"},
		Confidence: 0.9,
		Importance: 0.8,
		Embedding:  []float64{1, 0},
		Source:     "user",
	})
	if err != nil {
		t.Fatalf("insert memory: %v", err)
	}

	merged, err := store.UpsertMemory(ctx, Candidate{
		Type:       Preference,
		Scope:      ScopeUser,
		Subject:    "tone",
		Body:       "This lower-confidence body should not replace the first one.",
		Keywords:   []string{"slack"},
		Confidence: 0.2,
		Importance: 0.85,
		Source:     "assistant",
	})
	if err != nil {
		t.Fatalf("merge memory: %v", err)
	}
	if merged.Body != item.Body || len(merged.Keywords) < 2 {
		t.Fatalf("unexpected merged memory %#v", merged)
	}

	results, err := store.MemoriesByTypes(ctx, []Type{Preference}, 0.5, 5)
	if err != nil || len(results) != 1 {
		t.Fatalf("unexpected memories by type %#v err=%v", results, err)
	}

	emptyStore := newTestStore(t, time.Now)
	t.Cleanup(func() { _ = emptyStore.Close() })
	if hits, err := emptyStore.SearchMemories(ctx, "missing", nil, 5); err != nil || len(hits) != 0 {
		t.Fatalf("expected empty search hits, got %#v err=%v", hits, err)
	}
}

func TestStorePersonaAndReminderHelpers(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, time.Now)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	if err := store.EnsurePersonaLayer(ctx, "stable_identity", "seed", "test"); err != nil {
		t.Fatalf("ensure persona layer: %v", err)
	}
	initial, err := store.GetPersonaLayer(ctx, "stable_identity")
	if err != nil {
		t.Fatalf("get persona layer: %v", err)
	}
	if err := store.EnsurePersonaLayer(ctx, "stable_identity", "seed", "test"); err != nil {
		t.Fatalf("ensure persona layer second time: %v", err)
	}
	same, err := store.UpdatePersonaLayer(ctx, "stable_identity", "seed", "no-op", "test")
	if err != nil {
		t.Fatalf("update same persona layer: %v", err)
	}
	if same.Revision != initial.Revision {
		t.Fatalf("expected unchanged revision, got %#v", same)
	}

	parsed, err := parseOptionalReminderTime(sql.NullString{Valid: true, String: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil || parsed == nil {
		t.Fatalf("unexpected optional reminder parse result %v err=%v", parsed, err)
	}
	if parsed, err := parseOptionalReminderTime(sql.NullString{}); err != nil || parsed != nil {
		t.Fatalf("expected empty optional reminder time, got %v err=%v", parsed, err)
	}
	if _, err := parseOptionalReminderTime(sql.NullString{Valid: true, String: "bad-time"}); err == nil {
		t.Fatal("expected invalid reminder time to fail")
	}
}

func TestStoreScoringAndEncodingHelpers(t *testing.T) {
	t.Parallel()

	item := Item{}
	if err := decodeKeywords(`["alpha","beta"]`, &item); err != nil || len(item.Keywords) != 2 {
		t.Fatalf("unexpected decoded keywords %#v err=%v", item.Keywords, err)
	}
	if err := decodeKeywords("", &item); err != nil {
		t.Fatalf("expected blank keywords to be ignored: %v", err)
	}
	if err := decodeKeywords(`{`, &item); err == nil {
		t.Fatal("expected invalid keywords JSON to fail")
	}

	if err := decodeEmbedding(`[1,2]`, &item); err != nil || len(item.Embedding) != 2 {
		t.Fatalf("unexpected decoded embedding %#v err=%v", item.Embedding, err)
	}
	if err := decodeEmbedding("", &item); err != nil {
		t.Fatalf("expected blank embedding to be ignored: %v", err)
	}
	if err := decodeEmbedding(`bad`, &item); err == nil {
		t.Fatal("expected invalid embedding JSON to fail")
	}

	if got := summarise(strings.Repeat("a", 40), 10); !strings.HasSuffix(got, "…") {
		t.Fatalf("unexpected summary %q", got)
	}
	if got := summarise("short", 10); got != "short" {
		t.Fatalf("unexpected short summary %q", got)
	}

	marshalled, err := marshalFloatSlice([]float64{1, 2})
	if err != nil {
		t.Fatalf("marshal float slice: %v", err)
	}
	unmarshalled, err := unmarshalFloatSlice(marshalled)
	if err != nil || len(unmarshalled) != 2 {
		t.Fatalf("unexpected unmarshal %#v err=%v", unmarshalled, err)
	}
	if _, err := unmarshalFloatSlice("bad"); err == nil {
		t.Fatal("expected invalid float slice to fail")
	}
	if maxFloat(2, 1) != 2 {
		t.Fatal("expected maxFloat to keep the larger value")
	}

	now := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	general, squad := scoreEpisodes([]Episode{
		{Source: "user", Summary: "rook update", Text: "rook update", CreatedAt: now},
		{Source: "squad0", Summary: "rook update", Text: "rook update", CreatedAt: now},
		{Source: "user", Summary: "other", Text: "other", CreatedAt: now.AddDate(0, 0, -365)},
	}, "rook update", now)
	if len(general) == 0 || len(squad) == 0 {
		t.Fatalf("expected general and squad episode hits, got %#v / %#v", general, squad)
	}

	items := extractItems([]SearchHit{{Item: Item{Subject: "a"}}, {Item: Item{Subject: "b"}}})
	if len(items) != 2 {
		t.Fatalf("unexpected extracted items %#v", items)
	}
	episodes := extractEpisodes([]EpisodeHit{{Episode: Episode{Summary: "a"}}})
	if len(episodes) != 1 {
		t.Fatalf("unexpected extracted episodes %#v", episodes)
	}
}
