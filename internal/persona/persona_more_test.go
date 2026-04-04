package persona

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JR-G/rook/internal/memory"
)

func TestConsolidateIfDueAndSnapshotBranches(t *testing.T) {
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
			t.Fatalf("write file: %v", err)
		}
	}

	currentTime := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	store, err := memory.OpenWithClock(filepath.Join(tempDir, "rook.sqlite"), func() time.Time { return currentTime })
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := New(store, corePath, stablePath, voicePath, 24*time.Hour, func() time.Time { return currentTime.Add(2 * time.Hour) })
	if err := manager.Seed(context.Background()); err != nil {
		t.Fatalf("seed manager: %v", err)
	}
	if err := manager.ConsolidateIfDue(context.Background()); err != nil {
		t.Fatalf("expected consolidate-if-due before interval to no-op: %v", err)
	}

	manager.now = func() time.Time { return currentTime.Add(48 * time.Hour) }
	if _, err := store.UpsertMemory(context.Background(), memory.Candidate{
		Type:       memory.Project,
		Scope:      memory.ScopeUser,
		Subject:    "rook",
		Body:       "Working on rook",
		Confidence: 0.9,
		Importance: 0.8,
	}); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}
	if err := manager.ConsolidateIfDue(context.Background()); err != nil {
		t.Fatalf("expected consolidate-if-due after interval to succeed: %v", err)
	}

	snapshot, err := manager.Snapshot(context.Background())
	if err != nil || snapshot.StableIdentity == "" {
		t.Fatalf("unexpected snapshot %#v err=%v", snapshot, err)
	}
}

func TestConsolidateAndSnapshotErrorBranches(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	corePath := filepath.Join(tempDir, "core.md")
	if err := os.WriteFile(corePath, []byte("core"), 0o600); err != nil {
		t.Fatalf("write core file: %v", err)
	}

	store, err := memory.OpenWithClock(filepath.Join(tempDir, "rook.sqlite"), time.Now)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := New(store, corePath, filepath.Join(tempDir, "missing-stable.md"), filepath.Join(tempDir, "missing-voice.md"), time.Hour, time.Now)
	if err := manager.Consolidate(context.Background()); err == nil {
		t.Fatal("expected consolidate with missing seed files to fail")
	}
	if _, err := manager.Snapshot(context.Background()); err == nil {
		t.Fatal("expected snapshot without persona layers to fail")
	}
}
