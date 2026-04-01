package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Open opens a SQLite store using the real wall clock.
func Open(path string) (*Store, error) {
	return OpenWithClock(path, time.Now)
}

// OpenWithClock opens a SQLite store with a custom clock.
func OpenWithClock(path string, now func() time.Time) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	store := &Store{db: db, now: now}
	if err := store.configure(); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}

	return s.db.Close()
}

// Health reports basic store reachability and counts.
func (s *Store) Health(ctx context.Context) (Health, error) {
	if err := s.db.PingContext(ctx); err != nil {
		return Health{Reachable: false}, err
	}

	var memoryCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_items WHERE archived_at IS NULL`).Scan(&memoryCount); err != nil {
		return Health{}, err
	}

	var episodeCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM episodes`).Scan(&episodeCount); err != nil {
		return Health{}, err
	}

	var reminderCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM reminders WHERE sent_at IS NULL`).Scan(&reminderCount); err != nil {
		return Health{}, err
	}

	return Health{
		Reachable:      true,
		MemoryCount:    memoryCount,
		EpisodeCount:   episodeCount,
		PendingReminds: reminderCount,
	}, nil
}

// RecordEpisode stores a conversation or observed Slack event.
func (s *Store) RecordEpisode(ctx context.Context, input EpisodeInput) (Episode, error) {
	now := s.now().UTC()
	summary := summarise(input.Text, 240)

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO episodes (
			channel_id, thread_ts, user_id, role, source, text, summary, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		input.ChannelID,
		input.ThreadTS,
		input.UserID,
		input.Role,
		input.Source,
		summarise(input.Text, 2000),
		summary,
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return Episode{}, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Episode{}, err
	}

	return Episode{
		ID:        id,
		ChannelID: input.ChannelID,
		ThreadTS:  input.ThreadTS,
		UserID:    input.UserID,
		Role:      input.Role,
		Source:    input.Source,
		Text:      summarise(input.Text, 2000),
		Summary:   summary,
		CreatedAt: now,
	}, nil
}

// PruneEpisodes removes older episodes beyond the retention window.
func (s *Store) PruneEpisodes(ctx context.Context, retentionDays int) error {
	if retentionDays <= 0 {
		return nil
	}

	cutoff := s.now().UTC().AddDate(0, 0, -retentionDays)
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM episodes
		WHERE created_at < ?
	`,
		cutoff.Format(time.RFC3339Nano),
	)

	return err
}

// Decay archives low-importance stale memories.
func (s *Store) Decay(ctx context.Context) error {
	cutoff := s.now().UTC().AddDate(0, 0, -120)
	_, err := s.db.ExecContext(ctx, `
		UPDATE memory_items
		SET archived_at = ?
		WHERE archived_at IS NULL
		  AND importance < 0.35
		  AND last_seen_at < ?
	`,
		s.now().UTC().Format(time.RFC3339Nano),
		cutoff.Format(time.RFC3339Nano),
	)

	return err
}

// UpsertMemory inserts or merges a durable memory item.
func (s *Store) UpsertMemory(ctx context.Context, candidate Candidate) (Item, error) {
	if strings.TrimSpace(candidate.Subject) == "" || strings.TrimSpace(candidate.Body) == "" {
		return Item{}, errors.New("memory subject and body must not be empty")
	}

	now := s.now().UTC()
	subject := normaliseSubject(candidate.Subject)
	keywordsJSON, err := json.Marshal(uniqueTokens(candidate.Keywords))
	if err != nil {
		return Item{}, err
	}

	embeddingJSON, err := marshalFloatSlice(candidate.Embedding)
	if err != nil {
		return Item{}, err
	}

	existing, err := s.lookupItem(ctx, candidate.Type, candidate.Scope, subject)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Item{}, err
	}

	if errors.Is(err, sql.ErrNoRows) {
		return s.insertMemory(ctx, candidate, subject, string(keywordsJSON), embeddingJSON, now)
	}

	return s.mergeMemory(ctx, existing, candidate, now)
}

func (s *Store) configure() error {
	statements := []string{
		`PRAGMA foreign_keys = ON;`,
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA busy_timeout = 5000;`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) migrate() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS memory_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			scope TEXT NOT NULL,
			subject TEXT NOT NULL,
			body TEXT NOT NULL,
			keywords TEXT NOT NULL,
			confidence REAL NOT NULL,
			importance REAL NOT NULL,
			embedding TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			archived_at TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_memory_items_lookup
			ON memory_items (type, scope, subject, updated_at DESC);`,
		`CREATE TABLE IF NOT EXISTS memory_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			memory_id INTEGER NOT NULL,
			event_type TEXT NOT NULL,
			detail TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS episodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id TEXT NOT NULL,
			thread_ts TEXT NOT NULL,
			user_id TEXT NOT NULL,
			role TEXT NOT NULL,
			source TEXT NOT NULL,
			text TEXT NOT NULL,
			summary TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_episodes_created_at
			ON episodes (created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_episodes_source_created_at
			ON episodes (source, created_at DESC);`,
		`CREATE TABLE IF NOT EXISTS reminders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id TEXT NOT NULL,
			thread_ts TEXT NOT NULL,
			message TEXT NOT NULL,
			due_at TEXT NOT NULL,
			created_by TEXT NOT NULL,
			created_at TEXT NOT NULL,
			sent_at TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_reminders_due
			ON reminders (sent_at, due_at);`,
		`CREATE TABLE IF NOT EXISTS persona_profiles (
			layer TEXT PRIMARY KEY,
			revision INTEGER NOT NULL,
			content TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS persona_revisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			layer TEXT NOT NULL,
			revision INTEGER NOT NULL,
			content TEXT NOT NULL,
			reason TEXT NOT NULL,
			source TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
	}

	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}

	return nil
}
