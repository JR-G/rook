package persona

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JR-G/rook/internal/memory"
)

func TestBuildPersonaHelpers(t *testing.T) {
	t.Parallel()

	stable := buildStableIdentity("seed", []memory.Item{
		{Body: "Prefer direct replies."},
	})
	if !strings.Contains(stable, "Prefer direct replies.") {
		t.Fatalf("unexpected stable identity %q", stable)
	}

	voice := buildVoice("seed", []memory.Item{
		{Type: memory.CommunicationStyleNote, Body: "Use bullets when useful."},
	}, []memory.Episode{
		{Source: "user", Text: "Short question?"},
		{Source: "assistant", Text: "ignored"},
	})
	if !strings.Contains(voice, "Use bullets when useful.") {
		t.Fatalf("unexpected voice %q", voice)
	}

	derived := appendEpisodeVoiceNotes(nil, 2, 200, 2, 2)
	if len(derived) == 0 {
		t.Fatal("expected voice notes to be derived")
	}
}

func TestPersonaErrorPaths(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	store, err := memory.OpenWithClock(filepath.Join(tempDir, "rook.sqlite"), time.Now)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := New(
		store,
		filepath.Join(tempDir, "missing-core.md"),
		filepath.Join(tempDir, "missing-stable.md"),
		filepath.Join(tempDir, "missing-voice.md"),
		time.Hour,
		time.Now,
	)
	if err := manager.Seed(context.Background()); err == nil {
		t.Fatal("expected missing seed files to fail")
	}
	if _, err := manager.Snapshot(context.Background()); err == nil {
		t.Fatal("expected snapshot without files to fail")
	}
}
