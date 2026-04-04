package persona

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/JR-G/rook/internal/memory"
)

func TestRenderSnapshotAndConsolidateFromEvidence(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	corePath := filepath.Join(tempDir, "core.md")
	stablePath := filepath.Join(tempDir, "stable.md")
	voicePath := filepath.Join(tempDir, "voice.md")
	for path, content := range map[string]string{
		corePath:   "Protect the user and stay grounded.",
		stablePath: "Stable seed",
		voicePath:  "Voice seed",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write persona file: %v", err)
		}
	}

	currentTime := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	store, err := memory.OpenWithClock(filepath.Join(tempDir, "rook.sqlite"), func() time.Time { return currentTime })
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := New(store, corePath, stablePath, voicePath, time.Hour, func() time.Time { return currentTime.Add(2 * time.Hour) })
	if err := manager.Seed(context.Background()); err != nil {
		t.Fatalf("seed manager: %v", err)
	}

	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.Core == "" || snapshot.StableIdentity == "" || snapshot.EvolvingVoice == "" {
		t.Fatalf("unexpected snapshot %#v", snapshot)
	}

	prompt, err := manager.RenderSystemPrompt(context.Background())
	if err != nil {
		t.Fatalf("render system prompt: %v", err)
	}
	if !strings.Contains(prompt, "Core constitution:") || !strings.Contains(prompt, "Stable identity:") {
		t.Fatalf("unexpected system prompt %q", prompt)
	}
	if strings.Contains(prompt, "<final>") {
		t.Fatalf("prompt should not instruct final tags anymore: %q", prompt)
	}
	if !strings.Contains(prompt, "structured response") {
		t.Fatalf("prompt should describe structured runtime handling: %q", prompt)
	}

	candidates := []memory.Candidate{
		{
			Type:       memory.Preference,
			Scope:      memory.ScopeUser,
			Subject:    "tone",
			Body:       "Prefer direct replies.",
			Confidence: 0.95,
			Importance: 0.8,
		},
		{
			Type:       memory.CommunicationStyleNote,
			Scope:      memory.ScopeUser,
			Subject:    "lists",
			Body:       "Use bullets when they help.",
			Confidence: 0.96,
			Importance: 0.85,
		},
		{
			Type:       memory.Project,
			Scope:      memory.ScopeUser,
			Subject:    "rook",
			Body:       "Rook is the local always-on Slack agent.",
			Confidence: 0.9,
			Importance: 0.75,
		},
	}
	for _, candidate := range candidates {
		if _, err := store.UpsertMemory(context.Background(), candidate); err != nil {
			t.Fatalf("upsert memory: %v", err)
		}
	}

	for _, episode := range []memory.EpisodeInput{
		{
			ChannelID: "D1",
			ThreadTS:  "1.0",
			UserID:    "U1",
			Role:      "user",
			Source:    "user",
			Text:      "Can you keep it short?\n- Option A\n- Option B",
		},
		{
			ChannelID: "D1",
			ThreadTS:  "1.0",
			UserID:    "U1",
			Role:      "user",
			Source:    "user",
			Text:      "What tradeoffs should I consider?",
		},
	} {
		if _, err := store.RecordEpisode(context.Background(), episode); err != nil {
			t.Fatalf("record episode: %v", err)
		}
	}

	if err := manager.Consolidate(context.Background()); err != nil {
		t.Fatalf("consolidate: %v", err)
	}

	stableProfile, err := store.GetPersonaLayer(context.Background(), stableLayer)
	if err != nil {
		t.Fatalf("get stable profile: %v", err)
	}
	if stableProfile.Revision != 2 || !strings.Contains(stableProfile.Content, "Consolidated durable cues") {
		t.Fatalf("unexpected stable profile %#v", stableProfile)
	}

	voiceProfile, err := store.GetPersonaLayer(context.Background(), voiceLayer)
	if err != nil {
		t.Fatalf("get voice profile: %v", err)
	}
	if voiceProfile.Revision != 2 {
		t.Fatalf("unexpected voice revision %#v", voiceProfile)
	}
	if !strings.Contains(voiceProfile.Content, "Use bullets when they help.") {
		t.Fatalf("expected style note in voice profile %q", voiceProfile.Content)
	}
	if !strings.Contains(voiceProfile.Content, "Structured lists are often welcome") {
		t.Fatalf("expected episode-derived list note in voice profile %q", voiceProfile.Content)
	}
	if !strings.Contains(voiceProfile.Content, "Surface tradeoffs and next actions clearly") {
		t.Fatalf("expected episode-derived tradeoff note in voice profile %q", voiceProfile.Content)
	}
}

func TestSeedSnapshotAndConsolidateErrorBranches(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	corePath := filepath.Join(tempDir, "core.md")
	stablePath := filepath.Join(tempDir, "stable.md")
	voicePath := filepath.Join(tempDir, "voice.md")
	for path, content := range map[string]string{
		corePath:   "core",
		stablePath: "stable seed",
		voicePath:  "voice seed",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write persona file: %v", err)
		}
	}

	currentTime := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)

	closedStore, err := memory.OpenWithClock(filepath.Join(tempDir, "closed.sqlite"), func() time.Time { return currentTime })
	if err != nil {
		t.Fatalf("open closed store: %v", err)
	}
	if err := closedStore.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	manager := New(closedStore, corePath, stablePath, voicePath, time.Hour, func() time.Time { return currentTime })
	if err := manager.Seed(context.Background()); err == nil {
		t.Fatal("expected Seed to fail when the store is closed")
	}

	snapshotStore, err := memory.OpenWithClock(filepath.Join(tempDir, "snapshot.sqlite"), func() time.Time { return currentTime })
	if err != nil {
		t.Fatalf("open snapshot store: %v", err)
	}
	t.Cleanup(func() { _ = snapshotStore.Close() })
	manager = New(snapshotStore, corePath, stablePath, voicePath, time.Hour, func() time.Time { return currentTime })
	if err := manager.Seed(context.Background()); err != nil {
		t.Fatalf("seed manager: %v", err)
	}
	if _, err := snapshotStore.UpdatePersonaLayer(context.Background(), stableLayer, "stable next", "reason", "test"); err != nil {
		t.Fatalf("update stable layer: %v", err)
	}
	if _, err := storeDB(snapshotStore).ExecContext(context.Background(), `
		UPDATE persona_profiles
		SET updated_at = ?
		WHERE layer = ?
	`, "bad-time", voiceLayer); err != nil {
		t.Fatalf("corrupt voice layer timestamp: %v", err)
	}
	if _, err := manager.Snapshot(context.Background()); err == nil {
		t.Fatal("expected Snapshot to fail when the voice layer timestamp is malformed")
	}

	memoryStore, err := memory.OpenWithClock(filepath.Join(tempDir, "memory.sqlite"), func() time.Time { return currentTime })
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	t.Cleanup(func() { _ = memoryStore.Close() })
	manager = New(memoryStore, corePath, stablePath, voicePath, time.Hour, func() time.Time { return currentTime })
	if err := manager.Seed(context.Background()); err != nil {
		t.Fatalf("seed memory manager: %v", err)
	}
	if _, err := storeDB(memoryStore).ExecContext(context.Background(), `
		INSERT INTO memory_items (
			type, scope, subject, body, keywords, confidence, importance, embedding, source,
			created_at, updated_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		string(memory.Preference),
		memory.ScopeUser,
		"tone",
		"Prefer direct replies.",
		`["tone"]`,
		0.95,
		0.8,
		`[]`,
		"user",
		"bad-time",
		"bad-time",
		"bad-time",
	); err != nil {
		t.Fatalf("insert malformed memory row: %v", err)
	}
	if err := manager.Consolidate(context.Background()); err == nil {
		t.Fatal("expected Consolidate to fail when durable memory is malformed")
	}

	episodeStore, err := memory.OpenWithClock(filepath.Join(tempDir, "episode.sqlite"), func() time.Time { return currentTime })
	if err != nil {
		t.Fatalf("open episode store: %v", err)
	}
	t.Cleanup(func() { _ = episodeStore.Close() })
	manager = New(episodeStore, corePath, stablePath, voicePath, time.Hour, func() time.Time { return currentTime })
	if err := manager.Seed(context.Background()); err != nil {
		t.Fatalf("seed episode manager: %v", err)
	}
	if _, err := storeDB(episodeStore).ExecContext(context.Background(), `
		INSERT INTO episodes (
			channel_id, thread_ts, user_id, role, source, text, summary, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, "C1", "1.0", "U1", "user", "user", "hello", "hello", "bad-time"); err != nil {
		t.Fatalf("insert malformed episode row: %v", err)
	}
	if err := manager.Consolidate(context.Background()); err == nil {
		t.Fatal("expected Consolidate to fail when episode history is malformed")
	}

	revisionStore, err := memory.OpenWithClock(filepath.Join(tempDir, "revision.sqlite"), func() time.Time { return currentTime })
	if err != nil {
		t.Fatalf("open revision store: %v", err)
	}
	t.Cleanup(func() { _ = revisionStore.Close() })
	manager = New(revisionStore, corePath, stablePath, voicePath, time.Hour, func() time.Time { return currentTime })
	if err := manager.Seed(context.Background()); err != nil {
		t.Fatalf("seed revision manager: %v", err)
	}
	if _, err := revisionStore.UpsertMemory(context.Background(), memory.Candidate{
		Type:       memory.Preference,
		Scope:      memory.ScopeUser,
		Subject:    "tone",
		Body:       "Prefer direct replies.",
		Confidence: 0.95,
		Importance: 0.8,
	}); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}
	if _, err := storeDB(revisionStore).ExecContext(context.Background(), `DROP TABLE persona_revisions`); err != nil {
		t.Fatalf("drop persona_revisions table: %v", err)
	}
	if err := manager.Consolidate(context.Background()); err == nil {
		t.Fatal("expected Consolidate to fail when persona revisions cannot be recorded")
	}
}

func storeDB(store *memory.Store) *sql.DB {
	field := reflect.ValueOf(store).Elem().FieldByName("db")
	opened := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface()
	database, ok := opened.(*sql.DB)
	if !ok {
		panic("expected *sql.DB field")
	}

	return database
}
