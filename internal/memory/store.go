package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// MemoryType classifies a durable memory record.
type MemoryType string

// Memory scopes.
const (
	ScopeUser      = "user"
	ScopeAgent     = "agent"
	ScopeWorkspace = "workspace"
)

// Memory item types.
const (
	Fact                   MemoryType = "fact"
	Preference             MemoryType = "preference"
	Person                 MemoryType = "person"
	Project                MemoryType = "project"
	Decision               MemoryType = "decision"
	Commitment             MemoryType = "commitment"
	RelationshipNote       MemoryType = "relationship_note"
	CommunicationStyleNote MemoryType = "communication_style_note"
	OperatingPattern       MemoryType = "operating_pattern"
)

// Item is a durable memory record.
type Item struct {
	ID         int64
	Type       MemoryType
	Scope      string
	Subject    string
	Body       string
	Keywords   []string
	Confidence float64
	Importance float64
	Embedding  []float64
	Source     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastSeenAt time.Time
}

// Candidate is a proposed durable memory write.
type Candidate struct {
	Type       MemoryType
	Scope      string
	Subject    string
	Body       string
	Keywords   []string
	Confidence float64
	Importance float64
	Embedding  []float64
	Source     string
}

// Episode records one interaction event.
type Episode struct {
	ID        int64
	ChannelID string
	ThreadTS  string
	UserID    string
	Role      string
	Source    string
	Text      string
	Summary   string
	CreatedAt time.Time
}

// EpisodeInput contains the fields needed to store an episode.
type EpisodeInput struct {
	ChannelID string
	ThreadTS  string
	UserID    string
	Role      string
	Source    string
	Text      string
}

// Reminder is a persisted reminder.
type Reminder struct {
	ID        int64
	ChannelID string
	ThreadTS  string
	Message   string
	DueAt     time.Time
	CreatedBy string
	CreatedAt time.Time
	SentAt    *time.Time
}

// ReminderInput contains the fields needed to create a reminder.
type ReminderInput struct {
	ChannelID string
	ThreadTS  string
	Message   string
	DueAt     time.Time
	CreatedBy string
}

// RetrievalLimits configures prompt memory injection.
type RetrievalLimits struct {
	MaxPromptItems  int
	MaxEpisodeItems int
}

// RetrievalContext groups the injected memory by role.
type RetrievalContext struct {
	UserFacts      []Item
	WorkingContext []Item
	Episodes       []Episode
	Squad0Episodes []Episode
}

// SearchHit is a scored durable memory result.
type SearchHit struct {
	Item  Item
	Score float64
}

// EpisodeHit is a scored episode result.
type EpisodeHit struct {
	Episode Episode
	Score   float64
}

// PersonaProfile is the current snapshot of a persona layer.
type PersonaProfile struct {
	Layer     string
	Revision  int
	Content   string
	UpdatedAt time.Time
}

// Health describes the database state.
type Health struct {
	Reachable      bool
	MemoryCount    int
	EpisodeCount   int
	PendingReminds int
}

// Store manages all local persistence.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

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
		result, execErr := s.db.ExecContext(ctx, `
			INSERT INTO memory_items (
				type, scope, subject, body, keywords, confidence, importance, embedding, source,
				created_at, updated_at, last_seen_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			string(candidate.Type),
			candidate.Scope,
			subject,
			candidate.Body,
			string(keywordsJSON),
			candidate.Confidence,
			candidate.Importance,
			embeddingJSON,
			candidate.Source,
			now.Format(time.RFC3339Nano),
			now.Format(time.RFC3339Nano),
			now.Format(time.RFC3339Nano),
		)
		if execErr != nil {
			return Item{}, execErr
		}

		id, execErr := result.LastInsertId()
		if execErr != nil {
			return Item{}, execErr
		}

		item := Item{
			ID:         id,
			Type:       candidate.Type,
			Scope:      candidate.Scope,
			Subject:    subject,
			Body:       candidate.Body,
			Keywords:   uniqueTokens(candidate.Keywords),
			Confidence: candidate.Confidence,
			Importance: candidate.Importance,
			Embedding:  candidate.Embedding,
			Source:     candidate.Source,
			CreatedAt:  now,
			UpdatedAt:  now,
			LastSeenAt: now,
		}
		if logErr := s.logMemoryEvent(ctx, item.ID, "insert", candidate.Body); logErr != nil {
			return Item{}, logErr
		}

		return item, nil
	}

	mergedBody := existing.Body
	if candidate.Confidence >= existing.Confidence*0.85 && strings.TrimSpace(candidate.Body) != "" {
		mergedBody = candidate.Body
	}

	mergedKeywords := mergeUnique(existing.Keywords, candidate.Keywords)
	mergedEmbedding := existing.Embedding
	if len(candidate.Embedding) > 0 {
		mergedEmbedding = candidate.Embedding
	}

	mergedKeywordsJSON, err := json.Marshal(mergedKeywords)
	if err != nil {
		return Item{}, err
	}

	mergedEmbeddingJSON, err := marshalFloatSlice(mergedEmbedding)
	if err != nil {
		return Item{}, err
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE memory_items
		SET body = ?, keywords = ?, confidence = ?, importance = ?, embedding = ?, source = ?,
			updated_at = ?, last_seen_at = ?
		WHERE id = ?
	`,
		mergedBody,
		string(mergedKeywordsJSON),
		maxFloat(existing.Confidence, candidate.Confidence),
		maxFloat(existing.Importance, candidate.Importance),
		mergedEmbeddingJSON,
		candidate.Source,
		now.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
		existing.ID,
	); err != nil {
		return Item{}, err
	}

	existing.Body = mergedBody
	existing.Keywords = mergedKeywords
	existing.Confidence = maxFloat(existing.Confidence, candidate.Confidence)
	existing.Importance = maxFloat(existing.Importance, candidate.Importance)
	existing.Embedding = mergedEmbedding
	existing.Source = candidate.Source
	existing.UpdatedAt = now
	existing.LastSeenAt = now
	if err := s.logMemoryEvent(ctx, existing.ID, "merge", candidate.Body); err != nil {
		return Item{}, err
	}

	return existing, nil
}

// SearchContext retrieves only the most relevant prompt memory.
func (s *Store) SearchContext(ctx context.Context, query string, queryEmbedding []float64, limits RetrievalLimits) (RetrievalContext, error) {
	items, err := s.loadItems(ctx)
	if err != nil {
		return RetrievalContext{}, err
	}

	itemHits := scoreItems(items, query, queryEmbedding, s.now().UTC())
	userFacts := make([]SearchHit, 0, len(itemHits))
	working := make([]SearchHit, 0, len(itemHits))

	for _, hit := range itemHits {
		switch hit.Item.Type {
		case Fact, Preference, RelationshipNote, CommunicationStyleNote, OperatingPattern:
			userFacts = append(userFacts, hit)
		case Person, Project, Decision, Commitment:
			working = append(working, hit)
		}
	}

	episodes, err := s.loadEpisodes(ctx, 200)
	if err != nil {
		return RetrievalContext{}, err
	}
	episodeHits, squad0Hits := scoreEpisodes(episodes, query, s.now().UTC())

	return RetrievalContext{
		UserFacts:      extractItems(topNItems(userFacts, limits.MaxPromptItems/2, func(hit SearchHit) float64 { return hit.Score })),
		WorkingContext: extractItems(topNItems(working, limits.MaxPromptItems/2, func(hit SearchHit) float64 { return hit.Score })),
		Episodes:       extractEpisodes(topNItems(episodeHits, limits.MaxEpisodeItems, func(hit EpisodeHit) float64 { return hit.Score })),
		Squad0Episodes: extractEpisodes(topNItems(squad0Hits, limits.MaxEpisodeItems, func(hit EpisodeHit) float64 { return hit.Score })),
	}, nil
}

// SearchMemories returns scored memory hits for the memory command.
func (s *Store) SearchMemories(ctx context.Context, query string, queryEmbedding []float64, limit int) ([]SearchHit, error) {
	items, err := s.loadItems(ctx)
	if err != nil {
		return nil, err
	}

	hits := scoreItems(items, query, queryEmbedding, s.now().UTC())

	return topNItems(hits, limit, func(hit SearchHit) float64 { return hit.Score }), nil
}

// ListRecentMemories returns recently updated durable memories.
func (s *Store) ListRecentMemories(ctx context.Context, limit int) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, type, scope, subject, body, keywords, confidence, importance, embedding, source,
			created_at, updated_at, last_seen_at
		FROM memory_items
		WHERE archived_at IS NULL
		ORDER BY updated_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Item, 0, limit)
	for rows.Next() {
		item, scanErr := scanItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}

	return items, rows.Err()
}

// AddReminder creates a persisted reminder.
func (s *Store) AddReminder(ctx context.Context, input ReminderInput) (Reminder, error) {
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO reminders (
			channel_id, thread_ts, message, due_at, created_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`,
		input.ChannelID,
		input.ThreadTS,
		input.Message,
		input.DueAt.UTC().Format(time.RFC3339Nano),
		input.CreatedBy,
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return Reminder{}, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Reminder{}, err
	}

	return Reminder{
		ID:        id,
		ChannelID: input.ChannelID,
		ThreadTS:  input.ThreadTS,
		Message:   input.Message,
		DueAt:     input.DueAt.UTC(),
		CreatedBy: input.CreatedBy,
		CreatedAt: now,
	}, nil
}

// DueReminders returns reminders that should fire now.
func (s *Store) DueReminders(ctx context.Context, now time.Time, limit int) ([]Reminder, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel_id, thread_ts, message, due_at, created_by, created_at, sent_at
		FROM reminders
		WHERE sent_at IS NULL
		  AND due_at <= ?
		ORDER BY due_at ASC
		LIMIT ?
	`,
		now.UTC().Format(time.RFC3339Nano),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	reminders := make([]Reminder, 0, limit)
	for rows.Next() {
		reminder, scanErr := scanReminder(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		reminders = append(reminders, reminder)
	}

	return reminders, rows.Err()
}

// MarkReminderSent marks a reminder as delivered.
func (s *Store) MarkReminderSent(ctx context.Context, reminderID int64, sentAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE reminders
		SET sent_at = ?
		WHERE id = ?
	`,
		sentAt.UTC().Format(time.RFC3339Nano),
		reminderID,
	)

	return err
}

// PendingReminderCount returns the number of outstanding reminders.
func (s *Store) PendingReminderCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM reminders
		WHERE sent_at IS NULL
	`).Scan(&count); err != nil {
		return 0, err
	}

	return count, nil
}

// EnsurePersonaLayer seeds a persona layer if it does not exist.
func (s *Store) EnsurePersonaLayer(ctx context.Context, layer, content, source string) error {
	_, err := s.GetPersonaLayer(ctx, layer)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	now := s.now().UTC()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO persona_profiles (layer, revision, content, updated_at)
		VALUES (?, 1, ?, ?)
	`,
		layer,
		content,
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return err
	}

	return s.insertPersonaRevision(ctx, layer, 1, content, "seed", source)
}

// GetPersonaLayer returns the current persona layer snapshot.
func (s *Store) GetPersonaLayer(ctx context.Context, layer string) (PersonaProfile, error) {
	var profile PersonaProfile
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT layer, revision, content, updated_at
		FROM persona_profiles
		WHERE layer = ?
	`,
		layer,
	).Scan(&profile.Layer, &profile.Revision, &profile.Content, &updatedAt)
	if err != nil {
		return PersonaProfile{}, err
	}

	profile.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return PersonaProfile{}, err
	}

	return profile, nil
}

// UpdatePersonaLayer writes a new revision if the content changed.
func (s *Store) UpdatePersonaLayer(ctx context.Context, layer, content, reason, source string) (PersonaProfile, error) {
	current, err := s.GetPersonaLayer(ctx, layer)
	if err != nil {
		return PersonaProfile{}, err
	}

	if strings.TrimSpace(current.Content) == strings.TrimSpace(content) {
		return current, nil
	}

	nextRevision := current.Revision + 1
	now := s.now().UTC()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE persona_profiles
		SET revision = ?, content = ?, updated_at = ?
		WHERE layer = ?
	`,
		nextRevision,
		content,
		now.Format(time.RFC3339Nano),
		layer,
	); err != nil {
		return PersonaProfile{}, err
	}

	if err := s.insertPersonaRevision(ctx, layer, nextRevision, content, reason, source); err != nil {
		return PersonaProfile{}, err
	}

	return PersonaProfile{
		Layer:     layer,
		Revision:  nextRevision,
		Content:   content,
		UpdatedAt: now,
	}, nil
}

// MemoriesByTypes returns memories filtered by type and minimum confidence.
func (s *Store) MemoriesByTypes(ctx context.Context, types []MemoryType, minConfidence float64, limit int) ([]Item, error) {
	items, err := s.loadItems(ctx)
	if err != nil {
		return nil, err
	}

	allowed := make(map[MemoryType]struct{}, len(types))
	for _, memoryType := range types {
		allowed[memoryType] = struct{}{}
	}

	filtered := make([]Item, 0, len(items))
	for _, item := range items {
		if _, ok := allowed[item.Type]; !ok {
			continue
		}
		if item.Confidence < minConfidence {
			continue
		}
		filtered = append(filtered, item)
	}

	sort.Slice(filtered, func(left, right int) bool {
		if filtered[left].Importance == filtered[right].Importance {
			return filtered[left].UpdatedAt.After(filtered[right].UpdatedAt)
		}

		return filtered[left].Importance > filtered[right].Importance
	})

	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	return filtered, nil
}

// RecentEpisodes returns the latest episodes.
func (s *Store) RecentEpisodes(ctx context.Context, limit int) ([]Episode, error) {
	return s.loadEpisodes(ctx, limit)
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

func (s *Store) lookupItem(ctx context.Context, memoryType MemoryType, scope, subject string) (Item, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, type, scope, subject, body, keywords, confidence, importance, embedding, source,
			created_at, updated_at, last_seen_at
		FROM memory_items
		WHERE type = ?
		  AND scope = ?
		  AND subject = ?
		  AND archived_at IS NULL
		ORDER BY updated_at DESC
		LIMIT 1
	`,
		string(memoryType),
		scope,
		subject,
	)

	return scanItem(row)
}

func (s *Store) loadItems(ctx context.Context) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, type, scope, subject, body, keywords, confidence, importance, embedding, source,
			created_at, updated_at, last_seen_at
		FROM memory_items
		WHERE archived_at IS NULL
		ORDER BY updated_at DESC
		LIMIT 1000
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Item, 0, 128)
	for rows.Next() {
		item, scanErr := scanItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}

	return items, rows.Err()
}

func (s *Store) loadEpisodes(ctx context.Context, limit int) ([]Episode, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel_id, thread_ts, user_id, role, source, text, summary, created_at
		FROM episodes
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	episodes := make([]Episode, 0, limit)
	for rows.Next() {
		var episode Episode
		var createdAt string
		if err := rows.Scan(
			&episode.ID,
			&episode.ChannelID,
			&episode.ThreadTS,
			&episode.UserID,
			&episode.Role,
			&episode.Source,
			&episode.Text,
			&episode.Summary,
			&createdAt,
		); err != nil {
			return nil, err
		}

		parsed, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		episode.CreatedAt = parsed
		episodes = append(episodes, episode)
	}

	return episodes, rows.Err()
}

func (s *Store) logMemoryEvent(ctx context.Context, memoryID int64, eventType, detail string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_events (memory_id, event_type, detail, created_at)
		VALUES (?, ?, ?, ?)
	`,
		memoryID,
		eventType,
		summarise(detail, 500),
		s.now().UTC().Format(time.RFC3339Nano),
	)

	return err
}

func (s *Store) insertPersonaRevision(ctx context.Context, layer string, revision int, content, reason, source string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO persona_revisions (layer, revision, content, reason, source, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`,
		layer,
		revision,
		content,
		reason,
		source,
		s.now().UTC().Format(time.RFC3339Nano),
	)

	return err
}

func scoreItems(items []Item, query string, queryEmbedding []float64, now time.Time) []SearchHit {
	queryTokens := tokenize(query)
	hits := make([]SearchHit, 0, len(items))
	for _, item := range items {
		candidateTokens := mergeUnique(tokenize(item.Subject), tokenize(item.Body), item.Keywords)
		score := 0.45*cosineSimilarity(queryEmbedding, item.Embedding) +
			0.35*keywordScore(queryTokens, candidateTokens) +
			0.15*recencyScore(item.LastSeenAt, now) +
			0.05*item.Importance
		if score < 0.08 {
			continue
		}

		hits = append(hits, SearchHit{
			Item:  item,
			Score: score,
		})
	}

	sort.Slice(hits, func(left, right int) bool {
		return hits[left].Score > hits[right].Score
	})

	return hits
}

func scoreEpisodes(episodes []Episode, query string, now time.Time) ([]EpisodeHit, []EpisodeHit) {
	queryTokens := tokenize(query)
	general := make([]EpisodeHit, 0, len(episodes))
	squad0 := make([]EpisodeHit, 0, len(episodes))
	for _, episode := range episodes {
		candidateTokens := mergeUnique(tokenize(episode.Summary), tokenize(episode.Text))
		score := 0.7*keywordScore(queryTokens, candidateTokens) + 0.3*recencyScore(episode.CreatedAt, now)
		if score < 0.1 {
			continue
		}

		hit := EpisodeHit{Episode: episode, Score: score}
		if episode.Source == "squad0" {
			squad0 = append(squad0, hit)
			continue
		}

		general = append(general, hit)
	}

	sort.Slice(general, func(left, right int) bool {
		return general[left].Score > general[right].Score
	})
	sort.Slice(squad0, func(left, right int) bool {
		return squad0[left].Score > squad0[right].Score
	})

	return general, squad0
}

func extractItems(hits []SearchHit) []Item {
	items := make([]Item, 0, len(hits))
	for _, hit := range hits {
		items = append(items, hit.Item)
	}

	return items
}

func extractEpisodes(hits []EpisodeHit) []Episode {
	episodes := make([]Episode, 0, len(hits))
	for _, hit := range hits {
		episodes = append(episodes, hit.Episode)
	}

	return episodes
}

func scanItem(scanner interface{ Scan(dest ...any) error }) (Item, error) {
	var item Item
	var keywordsJSON string
	var embeddingJSON string
	var createdAt string
	var updatedAt string
	var lastSeenAt string
	if err := scanner.Scan(
		&item.ID,
		&item.Type,
		&item.Scope,
		&item.Subject,
		&item.Body,
		&keywordsJSON,
		&item.Confidence,
		&item.Importance,
		&embeddingJSON,
		&item.Source,
		&createdAt,
		&updatedAt,
		&lastSeenAt,
	); err != nil {
		return Item{}, err
	}

	if keywordsJSON != "" {
		if err := json.Unmarshal([]byte(keywordsJSON), &item.Keywords); err != nil {
			return Item{}, err
		}
	}

	if embeddingJSON != "" {
		embedding, err := unmarshalFloatSlice(embeddingJSON)
		if err != nil {
			return Item{}, err
		}
		item.Embedding = embedding
	}

	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Item{}, err
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return Item{}, err
	}
	item.LastSeenAt, err = time.Parse(time.RFC3339Nano, lastSeenAt)
	if err != nil {
		return Item{}, err
	}

	return item, nil
}

func scanReminder(scanner interface{ Scan(dest ...any) error }) (Reminder, error) {
	var reminder Reminder
	var dueAt string
	var createdAt string
	var sentAt sql.NullString
	if err := scanner.Scan(
		&reminder.ID,
		&reminder.ChannelID,
		&reminder.ThreadTS,
		&reminder.Message,
		&dueAt,
		&reminder.CreatedBy,
		&createdAt,
		&sentAt,
	); err != nil {
		return Reminder{}, err
	}

	var err error
	reminder.DueAt, err = time.Parse(time.RFC3339Nano, dueAt)
	if err != nil {
		return Reminder{}, err
	}
	reminder.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Reminder{}, err
	}
	if sentAt.Valid {
		parsed, parseErr := time.Parse(time.RFC3339Nano, sentAt.String)
		if parseErr != nil {
			return Reminder{}, parseErr
		}
		reminder.SentAt = &parsed
	}

	return reminder, nil
}

func mergeUnique(groups ...[]string) []string {
	seen := make(map[string]struct{})
	merged := make([]string, 0)
	for _, group := range groups {
		for _, entry := range group {
			trimmed := strings.TrimSpace(strings.ToLower(entry))
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			merged = append(merged, trimmed)
		}
	}

	sort.Strings(merged)

	return merged
}

func uniqueTokens(tokens []string) []string {
	return mergeUnique(tokens)
}

func normaliseSubject(subject string) string {
	return strings.Join(tokenize(subject), " ")
}

func summarise(text string, limit int) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) <= limit {
		return trimmed
	}

	return strings.TrimSpace(trimmed[:limit-1]) + "…"
}

func marshalFloatSlice(values []float64) (string, error) {
	if len(values) == 0 {
		return "", nil
	}

	raw, err := json.Marshal(values)
	if err != nil {
		return "", err
	}

	return string(raw), nil
}

func unmarshalFloatSlice(input string) ([]float64, error) {
	var values []float64
	if err := json.Unmarshal([]byte(input), &values); err != nil {
		return nil, err
	}

	return values, nil
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}

	return right
}

func (s *Store) String() string {
	return fmt.Sprintf("memory.Store{%p}", s)
}
