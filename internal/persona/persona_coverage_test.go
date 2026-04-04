package persona

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JR-G/rook/internal/memory"
)

func TestSeedIsIdempotentAndConsolidateEmpty(t *testing.T) {
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

	store, err := memory.OpenWithClock(filepath.Join(tempDir, "rook.sqlite"), time.Now)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := New(store, corePath, stablePath, voicePath, time.Hour, time.Now)
	if err := manager.Seed(context.Background()); err != nil {
		t.Fatalf("seed manager: %v", err)
	}
	if err := manager.Seed(context.Background()); err != nil {
		t.Fatalf("seed manager second time: %v", err)
	}
	if err := manager.Consolidate(context.Background()); err != nil {
		t.Fatalf("consolidate empty manager: %v", err)
	}
}

func TestSeedFailsWhenVoiceSeedMissingAndConsolidateFailsOnStoreError(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	corePath := filepath.Join(tempDir, "core.md")
	stablePath := filepath.Join(tempDir, "stable.md")
	if err := os.WriteFile(corePath, []byte("core"), 0o600); err != nil {
		t.Fatalf("write core file: %v", err)
	}
	if err := os.WriteFile(stablePath, []byte("stable"), 0o600); err != nil {
		t.Fatalf("write stable file: %v", err)
	}

	store, err := memory.OpenWithClock(filepath.Join(tempDir, "rook.sqlite"), time.Now)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := New(store, corePath, stablePath, filepath.Join(tempDir, "missing-voice.md"), time.Hour, time.Now)
	if err := manager.Seed(context.Background()); err == nil {
		t.Fatal("expected missing voice seed file to fail")
	}

	voicePath := filepath.Join(tempDir, "voice.md")
	if err := os.WriteFile(voicePath, []byte("voice"), 0o600); err != nil {
		t.Fatalf("write voice file: %v", err)
	}
	manager = New(store, corePath, stablePath, voicePath, time.Hour, time.Now)
	if err := manager.Seed(context.Background()); err != nil {
		t.Fatalf("seed manager: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if err := manager.Consolidate(context.Background()); err == nil {
		t.Fatal("expected consolidate with closed store to fail")
	}
}

func TestConsolidateIfDueErrorsWithoutSeed(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	store, err := memory.OpenWithClock(filepath.Join(tempDir, "rook.sqlite"), time.Now)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := New(store, filepath.Join(tempDir, "core.md"), filepath.Join(tempDir, "stable.md"), filepath.Join(tempDir, "voice.md"), time.Hour, time.Now)
	if err := manager.ConsolidateIfDue(context.Background()); err == nil {
		t.Fatal("expected consolidate-if-due without seeded layers to fail")
	}
}
