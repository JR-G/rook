package memory

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestMalformedRowsSurfaceFromStoreQueries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	memoryStore := newTestStore(t, time.Now)
	t.Cleanup(func() { _ = memoryStore.Close() })
	_, err := memoryStore.writer.ExecContext(ctx, `
		INSERT INTO memory_items (
			type, scope, subject, body, keywords, confidence, importance, embedding, source,
			created_at, updated_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		string(Fact),
		ScopeUser,
		"name",
		"James",
		`["name"]`,
		0.9,
		0.8,
		`[1,0]`,
		"user",
		"bad-time",
		"bad-time",
		"bad-time",
	)
	if err != nil {
		t.Fatalf("insert malformed memory row: %v", err)
	}

	if _, err := memoryStore.SearchMemories(ctx, "name", []float64{1, 0}, 5); err == nil {
		t.Fatal("expected malformed memory row to break SearchMemories")
	}
	if _, err := memoryStore.SearchContext(ctx, "name", []float64{1, 0}, RetrievalLimits{MaxPromptItems: 2, MaxEpisodeItems: 1}); err == nil {
		t.Fatal("expected malformed memory row to break SearchContext")
	}
	if _, err := memoryStore.ListRecentMemories(ctx, 5); err == nil {
		t.Fatal("expected malformed memory row to break ListRecentMemories")
	}

	episodeStore := newTestStore(t, time.Now)
	t.Cleanup(func() { _ = episodeStore.Close() })
	_, err = episodeStore.writer.ExecContext(ctx, `
		INSERT INTO episodes (
			channel_id, thread_ts, user_id, role, source, text, summary, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"C1",
		"1.0",
		"U1",
		"user",
		"user",
		"hello",
		"hello",
		"bad-time",
	)
	if err != nil {
		t.Fatalf("insert malformed episode row: %v", err)
	}

	if _, err := episodeStore.RecentEpisodes(ctx, 5); err == nil {
		t.Fatal("expected malformed episode row to break RecentEpisodes")
	}
}

func TestPersonaParsingMergeAndStoreErrorBranches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	personaStore := newTestStore(t, time.Now)
	t.Cleanup(func() { _ = personaStore.Close() })
	_, err := personaStore.writer.ExecContext(ctx, `
		INSERT INTO persona_profiles (layer, revision, content, updated_at)
		VALUES (?, ?, ?, ?)
	`,
		"stable_identity",
		1,
		"seed",
		"bad-time",
	)
	if err != nil {
		t.Fatalf("insert malformed persona profile: %v", err)
	}

	if _, err := personaStore.GetPersonaLayer(ctx, "stable_identity"); err == nil {
		t.Fatal("expected malformed persona profile to break GetPersonaLayer")
	}
	if err := personaStore.EnsurePersonaLayer(ctx, "stable_identity", "seed", "test"); err == nil {
		t.Fatal("expected EnsurePersonaLayer to surface persona parse errors")
	}
	if _, err := personaStore.UpdatePersonaLayer(ctx, "stable_identity", "next", "reason", "test"); err == nil {
		t.Fatal("expected UpdatePersonaLayer to surface persona parse errors")
	}

	currentTime := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	mergeStore := newTestStore(t, func() time.Time { return currentTime })
	t.Cleanup(func() { _ = mergeStore.Close() })

	existing, err := mergeStore.UpsertMemory(ctx, Candidate{
		Type:       Preference,
		Scope:      ScopeUser,
		Subject:    "tone",
		Body:       "Prefer concise replies.",
		Keywords:   []string{"concise"},
		Embedding:  []float64{1, 0},
		Confidence: 0.5,
		Importance: 0.6,
		Source:     "user",
	})
	if err != nil {
		t.Fatalf("seed existing memory: %v", err)
	}

	merged, err := mergeStore.mergeMemory(ctx, existing, Candidate{
		Body:       "Prefer direct replies.",
		Keywords:   []string{"direct"},
		Embedding:  []float64{0, 1},
		Confidence: 0.95,
		Importance: 0.9,
		Source:     "assistant",
	}, currentTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("merge memory: %v", err)
	}
	if merged.Body != "Prefer direct replies." {
		t.Fatalf("expected replacement body, got %#v", merged)
	}
	if len(merged.Keywords) != 2 || len(merged.Embedding) != 2 {
		t.Fatalf("expected merged keywords and embedding, got %#v", merged)
	}

	if _, err := mergeStore.mergeMemory(ctx, existing, Candidate{
		Body:       "bad embedding",
		Embedding:  []float64{math.NaN()},
		Confidence: 0.99,
		Importance: 0.95,
		Source:     "assistant",
	}, currentTime.Add(2*time.Hour)); err == nil {
		t.Fatal("expected mergeMemory to fail on invalid embedding json")
	}

	if score := recencyScore(time.Time{}, currentTime); score != 0 {
		t.Fatalf("expected zero-time recency score, got %f", score)
	}
}

func TestHealthAndMigrateDirectFailures(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t, time.Now)

	if _, err := store.writer.ExecContext(ctx, `DROP TABLE memory_items`); err != nil {
		t.Fatalf("drop memory_items table: %v", err)
	}
	if _, err := store.Health(ctx); err == nil {
		t.Fatal("expected Health to fail when memory_items table is missing")
	}

	episodeStore := newTestStore(t, time.Now)
	t.Cleanup(func() { _ = episodeStore.Close() })
	if _, err := episodeStore.writer.ExecContext(ctx, `DROP TABLE episodes`); err != nil {
		t.Fatalf("drop episodes table: %v", err)
	}
	if _, err := episodeStore.Health(ctx); err == nil {
		t.Fatal("expected Health to fail when episodes table is missing")
	}

	if _, err := store.writer.ExecContext(ctx, `DROP TABLE reminders`); err != nil {
		t.Fatalf("drop reminders table: %v", err)
	}
	if _, err := store.Health(ctx); err == nil {
		t.Fatal("expected Health to fail when reminders table is missing")
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if err := store.migrate(); err == nil {
		t.Fatal("expected migrate on a closed store to fail")
	}
}

func TestExtractorScoringAndWriteFailureBranches(t *testing.T) {
	t.Parallel()

	if candidates := extractPreferenceCandidates("I prefer   ."); len(candidates) != 0 {
		t.Fatalf("expected empty preference candidates, got %#v", candidates)
	}
	if candidates := extractIdentityCandidates("my name is   "); len(candidates) != 0 {
		t.Fatalf("expected empty identity candidates, got %#v", candidates)
	}
	if candidates := extractProjectCandidates("I am working on   ."); len(candidates) != 0 {
		t.Fatalf("expected empty project candidates, got %#v", candidates)
	}
	if candidates := extractDecisionCandidates("Decision:   ."); len(candidates) != 0 {
		t.Fatalf("expected empty decision candidates, got %#v", candidates)
	}

	if score := keywordScore(nil, []string{"rook"}); score != 0 {
		t.Fatalf("expected empty keyword score, got %f", score)
	}
	if score := cosineSimilarity([]float64{0, 0}, []float64{1, 1}); score != 0 {
		t.Fatalf("expected zero-norm cosine similarity to be zero, got %f", score)
	}

	ctx := context.Background()
	currentTime := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)

	insertStore := newTestStore(t, func() time.Time { return currentTime })
	t.Cleanup(func() { _ = insertStore.Close() })
	if _, err := insertStore.writer.ExecContext(ctx, `DROP TABLE memory_events`); err != nil {
		t.Fatalf("drop memory_events table: %v", err)
	}
	if _, err := insertStore.insertMemory(ctx, Candidate{
		Type:       Fact,
		Scope:      ScopeUser,
		Subject:    "name",
		Body:       "James",
		Keywords:   []string{"name"},
		Confidence: 0.9,
		Importance: 0.8,
		Source:     "user",
	}, "name", `["name"]`, "", currentTime); err == nil {
		t.Fatal("expected insertMemory to fail when memory_events is missing")
	}

	mergeStore := newTestStore(t, func() time.Time { return currentTime })
	t.Cleanup(func() { _ = mergeStore.Close() })
	existing, err := mergeStore.UpsertMemory(ctx, Candidate{
		Type:       Preference,
		Scope:      ScopeUser,
		Subject:    "tone",
		Body:       "Prefer concise replies.",
		Keywords:   []string{"concise"},
		Embedding:  []float64{1, 0},
		Confidence: 0.6,
		Importance: 0.7,
		Source:     "user",
	})
	if err != nil {
		t.Fatalf("seed merge memory: %v", err)
	}
	if _, err := mergeStore.writer.ExecContext(ctx, `DROP TABLE memory_events`); err != nil {
		t.Fatalf("drop memory_events table: %v", err)
	}
	if _, err := mergeStore.mergeMemory(ctx, existing, Candidate{
		Body:       "Prefer direct replies.",
		Keywords:   []string{"direct"},
		Embedding:  []float64{0, 1},
		Confidence: 0.95,
		Importance: 0.9,
		Source:     "assistant",
	}, currentTime.Add(time.Hour)); err == nil {
		t.Fatal("expected mergeMemory to fail when memory_events is missing")
	}

	personaStore := newTestStore(t, func() time.Time { return currentTime })
	t.Cleanup(func() { _ = personaStore.Close() })
	if err := personaStore.EnsurePersonaLayer(ctx, "stable_identity", "seed", "test"); err != nil {
		t.Fatalf("ensure persona layer: %v", err)
	}
	if _, err := personaStore.writer.ExecContext(ctx, `DROP TABLE persona_revisions`); err != nil {
		t.Fatalf("drop persona_revisions table: %v", err)
	}
	if _, err := personaStore.UpdatePersonaLayer(ctx, "stable_identity", "next", "reason", "test"); err == nil {
		t.Fatal("expected UpdatePersonaLayer to fail when persona_revisions is missing")
	}
}
