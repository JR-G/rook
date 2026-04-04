package persona

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JR-G/rook/internal/memory"
)

func TestSeedSnapshotAndConsolidate(t *testing.T) {
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
			t.Fatalf("write file %s: %v", path, err)
		}
	}

	store, err := memory.OpenWithClock(filepath.Join(tempDir, "rook.sqlite"), func() time.Time {
		return time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := New(store, corePath, stablePath, voicePath, time.Hour, time.Now)
	ctx := context.Background()
	if err := manager.Seed(ctx); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	if _, err := store.UpsertMemory(ctx, memory.Candidate{
		Type:       memory.CommunicationStyleNote,
		Scope:      memory.ScopeUser,
		Subject:    "concise",
		Body:       "Prefer concise replies.",
		Confidence: 0.95,
		Importance: 0.9,
	}); err != nil {
		t.Fatalf("upsert memory failed: %v", err)
	}
	if _, err := store.RecordEpisode(ctx, memory.EpisodeInput{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Role:      "user",
		Source:    "user",
		Text:      "Can you keep this short?",
	}); err != nil {
		t.Fatalf("record episode failed: %v", err)
	}

	if err := manager.Consolidate(ctx); err != nil {
		t.Fatalf("consolidate failed: %v", err)
	}

	snapshot, err := manager.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if !strings.Contains(snapshot.Core, "core") {
		t.Fatalf("unexpected core snapshot %q", snapshot.Core)
	}
	if !strings.Contains(snapshot.EvolvingVoice, "Prefer concise replies.") {
		t.Fatalf("expected consolidated voice to include style note: %q", snapshot.EvolvingVoice)
	}
}

func TestRenderSystemPromptAndConsolidateIfDue(t *testing.T) {
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
			t.Fatalf("write file %s: %v", path, err)
		}
	}

	store, err := memory.OpenWithClock(filepath.Join(tempDir, "rook.sqlite"), time.Now)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := New(store, corePath, stablePath, voicePath, 24*time.Hour, time.Now)
	if err := manager.Seed(context.Background()); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	prompt, err := manager.RenderSystemPrompt(context.Background())
	if err != nil {
		t.Fatalf("render prompt: %v", err)
	}
	if !strings.Contains(prompt, "Core constitution") {
		t.Fatalf("unexpected prompt %q", prompt)
	}
	if strings.Contains(prompt, "<final>") {
		t.Fatalf("persona prompt should not require final tags: %q", prompt)
	}
	if !strings.Contains(prompt, "structured response") {
		t.Fatalf("persona prompt should describe structured output handling: %q", prompt)
	}
	if !strings.Contains(prompt, "canned slogans") {
		t.Fatalf("persona prompt should discourage canned slogans: %q", prompt)
	}
	if err := manager.ConsolidateIfDue(context.Background()); err != nil {
		t.Fatalf("consolidate if due: %v", err)
	}
}
