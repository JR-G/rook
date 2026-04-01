package memory

import (
	"context"
	"encoding/json"
	"time"
)

func (s *Store) insertMemory(
	ctx context.Context,
	candidate Candidate,
	subject string,
	keywordsJSON string,
	embeddingJSON string,
	now time.Time,
) (Item, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_items (
			type, scope, subject, body, keywords, confidence, importance, embedding, source,
			created_at, updated_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		string(candidate.Type),
		candidate.Scope,
		subject,
		candidate.Body,
		keywordsJSON,
		candidate.Confidence,
		candidate.Importance,
		embeddingJSON,
		candidate.Source,
		now.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return Item{}, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Item{}, err
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
	if err := s.logMemoryEvent(ctx, item.ID, "insert", candidate.Body); err != nil {
		return Item{}, err
	}

	return item, nil
}

func (s *Store) mergeMemory(ctx context.Context, existing Item, candidate Candidate, now time.Time) (Item, error) {
	mergedBody := existing.Body
	if candidate.Confidence >= existing.Confidence*0.85 && candidate.Body != "" {
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
