package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *Store) lookupItem(ctx context.Context, memoryType Type, scope, subject string) (Item, error) {
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
	defer func() {
		_ = rows.Close()
	}()

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
	defer func() {
		_ = rows.Close()
	}()

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

	if err := decodeKeywords(keywordsJSON, &item); err != nil {
		return Item{}, err
	}

	if err := decodeEmbedding(embeddingJSON, &item); err != nil {
		return Item{}, err
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

func decodeKeywords(keywordsJSON string, item *Item) error {
	if keywordsJSON == "" {
		return nil
	}

	return json.Unmarshal([]byte(keywordsJSON), &item.Keywords)
}

func decodeEmbedding(embeddingJSON string, item *Item) error {
	if embeddingJSON == "" {
		return nil
	}

	embedding, err := unmarshalFloatSlice(embeddingJSON)
	if err != nil {
		return err
	}

	item.Embedding = embedding

	return nil
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

	return sortedStrings(merged)
}

func sortedStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}

	sorted := append([]string(nil), values...)
	sort.Strings(sorted)

	return sorted
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
