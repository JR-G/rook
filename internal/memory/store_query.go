package memory

import (
	"context"
	"sort"
	"time"
)

const assistantEpisodeSource = "assistant"

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
	rows, err := s.reader.QueryContext(ctx, `
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
	defer func() {
		_ = rows.Close()
	}()

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

// MemoriesByTypes returns memories filtered by type and minimum confidence.
func (s *Store) MemoriesByTypes(ctx context.Context, types []Type, minConfidence float64, limit int) ([]Item, error) {
	items, err := s.loadItems(ctx)
	if err != nil {
		return nil, err
	}

	allowed := make(map[Type]struct{}, len(types))
	for _, memoryType := range types {
		allowed[memoryType] = struct{}{}
	}

	filtered := make([]Item, 0, len(items))
	for _, item := range items {
		if _, ok := allowed[item.Type]; !ok || item.Confidence < minConfidence {
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

// ThreadEpisodes returns the most recent episodes in one thread in chronological order.
func (s *Store) ThreadEpisodes(ctx context.Context, channelID, threadTS string, limit int) ([]Episode, error) {
	if limit <= 0 {
		return nil, nil
	}

	rows, err := s.reader.QueryContext(ctx, `
		SELECT id, channel_id, thread_ts, user_id, role, source, text, summary, created_at
		FROM episodes
		WHERE channel_id = ?
		  AND thread_ts = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, channelID, threadTS, limit)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for left, right := 0, len(episodes)-1; left < right; left, right = left+1, right-1 {
		episodes[left], episodes[right] = episodes[right], episodes[left]
	}

	return episodes, nil
}

// HasAssistantReplyInThread reports whether rook has already replied in a thread.
func (s *Store) HasAssistantReplyInThread(ctx context.Context, channelID, threadTS string) (bool, error) {
	var count int
	if err := s.reader.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM episodes
		WHERE channel_id = ?
		  AND thread_ts = ?
		  AND source = ?
	`, channelID, threadTS, assistantEpisodeSource).Scan(&count); err != nil {
		return false, err
	}

	return count > 0, nil
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

func scoreEpisodes(episodes []Episode, query string, now time.Time) (generalHits, squad0Hits []EpisodeHit) {
	queryTokens := tokenize(query)
	generalHits = make([]EpisodeHit, 0, len(episodes))
	squad0Hits = make([]EpisodeHit, 0, len(episodes))
	for _, episode := range episodes {
		if episode.Source == assistantEpisodeSource {
			continue
		}

		candidateTokens := mergeUnique(tokenize(episode.Summary), tokenize(episode.Text))
		score := 0.7*keywordScore(queryTokens, candidateTokens) + 0.3*recencyScore(episode.CreatedAt, now)
		if score < 0.1 {
			continue
		}

		hit := EpisodeHit{Episode: episode, Score: score}
		if episode.Source == "squad0" {
			squad0Hits = append(squad0Hits, hit)
			continue
		}

		generalHits = append(generalHits, hit)
	}

	sort.Slice(generalHits, func(left, right int) bool {
		return generalHits[left].Score > generalHits[right].Score
	})
	sort.Slice(squad0Hits, func(left, right int) bool {
		return squad0Hits[left].Score > squad0Hits[right].Score
	})

	return generalHits, squad0Hits
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
