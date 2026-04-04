package memory

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

type fakeScanner struct {
	values []any
	err    error
}

func (f fakeScanner) Scan(dest ...any) error {
	if f.err != nil {
		return f.err
	}
	for index := range dest {
		switch target := dest[index].(type) {
		case *int64:
			value, ok := f.values[index].(int64)
			if !ok {
				return errors.New("expected int64 scan value")
			}
			*target = value
		case *Type:
			value, ok := f.values[index].(Type)
			if !ok {
				return errors.New("expected memory type scan value")
			}
			*target = value
		case *string:
			value, ok := f.values[index].(string)
			if !ok {
				return errors.New("expected string scan value")
			}
			*target = value
		case *float64:
			value, ok := f.values[index].(float64)
			if !ok {
				return errors.New("expected float64 scan value")
			}
			*target = value
		case *sql.NullString:
			value, ok := f.values[index].(sql.NullString)
			if !ok {
				return errors.New("expected null string scan value")
			}
			*target = value
		default:
			panic("unsupported scan target")
		}
	}

	return nil
}

func TestClosedStoreQueryAndReminderErrors(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, time.Now)
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	ctx := context.Background()
	if _, err := store.SearchMemories(ctx, "query", nil, 5); err == nil {
		t.Fatal("expected closed-store SearchMemories to fail")
	}
	if _, err := store.SearchContext(ctx, "query", nil, RetrievalLimits{MaxPromptItems: 2, MaxEpisodeItems: 1}); err == nil {
		t.Fatal("expected closed-store SearchContext to fail")
	}
	if _, err := store.AddReminder(ctx, ReminderInput{}); err == nil {
		t.Fatal("expected closed-store AddReminder to fail")
	}
	if _, err := store.PendingReminderCount(ctx); err == nil {
		t.Fatal("expected closed-store PendingReminderCount to fail")
	}
	if _, err := store.RecordEpisode(ctx, EpisodeInput{}); err == nil {
		t.Fatal("expected closed-store RecordEpisode to fail")
	}
	if _, err := store.UpsertMemory(ctx, Candidate{Type: Fact, Scope: ScopeUser, Subject: "name", Body: "james"}); err == nil {
		t.Fatal("expected closed-store UpsertMemory to fail")
	}
	if _, err := store.ListRecentMemories(ctx, 5); err == nil {
		t.Fatal("expected closed-store ListRecentMemories to fail")
	}
	if _, err := store.DueReminders(ctx, time.Now().UTC(), 1); err == nil {
		t.Fatal("expected closed-store DueReminders to fail")
	}
	if err := store.EnsurePersonaLayer(ctx, "stable_identity", "seed", "test"); err == nil {
		t.Fatal("expected closed-store EnsurePersonaLayer to fail")
	}
	if _, err := store.UpdatePersonaLayer(ctx, "stable_identity", "seed", "reason", "test"); err == nil {
		t.Fatal("expected closed-store UpdatePersonaLayer to fail")
	}
}

func TestScanItemAndReminderErrorBranches(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	validReminderTime := sql.NullString{Valid: true, String: now}
	_, err := scanItem(fakeScanner{values: []any{
		int64(1), Fact, ScopeUser, "subject", "body", "{", 0.9, 0.8, "", "user", now, now, now,
	}})
	if err == nil {
		t.Fatal("expected invalid keywords JSON to fail")
	}

	_, err = scanItem(fakeScanner{values: []any{
		int64(1), Fact, ScopeUser, "subject", "body", `["a"]`, 0.9, 0.8, "bad", "user", now, now, now,
	}})
	if err == nil {
		t.Fatal("expected invalid embedding JSON to fail")
	}

	_, err = scanReminder(fakeScanner{values: []any{
		int64(1), "C1", "1.0", "stretch", "bad-time", "U1", now, validReminderTime,
	}})
	if err == nil {
		t.Fatal("expected invalid reminder due time to fail")
	}
}

func TestStorePersonaUpdateAndReminderCounts(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, time.Now)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	if err := store.EnsurePersonaLayer(ctx, "stable_identity", "seed", "test"); err != nil {
		t.Fatalf("ensure persona layer: %v", err)
	}
	updated, err := store.UpdatePersonaLayer(ctx, "stable_identity", "updated", "reason", "persona")
	if err != nil || updated.Revision != 2 {
		t.Fatalf("unexpected updated persona %#v err=%v", updated, err)
	}

	reminder, err := store.AddReminder(ctx, ReminderInput{
		ChannelID: "D1",
		ThreadTS:  "1.0",
		Message:   "stretch",
		DueAt:     time.Now().UTC().Add(-time.Minute),
		CreatedBy: "U1",
	})
	if err != nil {
		t.Fatalf("add reminder: %v", err)
	}
	if _, err := store.PendingReminderCount(ctx); err != nil {
		t.Fatalf("pending reminder count: %v", err)
	}
	if due, err := store.DueReminders(ctx, time.Now().UTC(), 1); err != nil || len(due) != 1 || due[0].ID != reminder.ID {
		t.Fatalf("unexpected due reminders %#v err=%v", due, err)
	}
}

func TestOpenWithClockDirectoryAndInsertMemoryFailure(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	if _, err := OpenWithClock(tempDir, time.Now); err == nil {
		t.Fatal("expected opening a directory path as a database to fail")
	}

	store := newTestStore(t, time.Now)
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if _, err := store.insertMemory(context.Background(), Candidate{
		Type:       Fact,
		Scope:      ScopeUser,
		Subject:    "name",
		Body:       "James",
		Confidence: 0.9,
		Importance: 0.8,
	}, "name", `["name"]`, "", time.Now().UTC()); err == nil {
		t.Fatal("expected insertMemory on closed store to fail")
	}
}

func TestScanHelpersReturnScannerError(t *testing.T) {
	t.Parallel()

	scannerErr := errors.New("scan failed")
	if _, err := scanItem(fakeScanner{err: scannerErr}); !errors.Is(err, scannerErr) {
		t.Fatalf("expected scanItem to return scanner error, got %v", err)
	}
	if _, err := scanReminder(fakeScanner{err: scannerErr}); !errors.Is(err, scannerErr) {
		t.Fatalf("expected scanReminder to return scanner error, got %v", err)
	}
}
