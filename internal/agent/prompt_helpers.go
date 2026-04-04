package agent

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/JR-G/rook/internal/memory"
)

type queryProfile struct {
	MetaReflection      bool
	ShortThreadFollowUp bool
}

func adjustRetrievalForQuery(
	query string,
	channelID string,
	threadTS string,
	threadEpisodes []memory.Episode,
	retrieval memory.RetrievalContext,
) memory.RetrievalContext {
	retrieval.Episodes = excludeThreadEpisodes(retrieval.Episodes, channelID, threadTS)
	if len(threadEpisodes) > 0 {
		retrieval.Episodes = nil
	}
	if analyseQuery(query, threadEpisodes).MetaReflection {
		retrieval.Episodes = nil
	}

	return retrieval
}

func analyseQuery(query string, threadEpisodes []memory.Episode) queryProfile {
	return queryProfile{
		MetaReflection:      isMetaReflectionQuery(query),
		ShortThreadFollowUp: isShortThreadFollowUp(query, threadEpisodes),
	}
}

func excludeThreadEpisodes(episodes []memory.Episode, channelID, threadTS string) []memory.Episode {
	if len(episodes) == 0 {
		return nil
	}

	filtered := make([]memory.Episode, 0, len(episodes))
	for _, episode := range episodes {
		if episode.ChannelID == channelID && episode.ThreadTS == threadTS {
			continue
		}
		filtered = append(filtered, episode)
	}

	return filtered
}

func isMetaReflectionQuery(query string) bool {
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	if lowerQuery == "" {
		return false
	}

	triggers := []string{
		"how are you",
		"how are you feeling",
		"how is it going",
		"what's on your mind",
		"what is on your mind",
		"what do you think",
		"how do you feel",
		"what's your view",
		"what is your view",
	}
	for _, trigger := range triggers {
		if strings.Contains(lowerQuery, trigger) {
			return true
		}
	}

	return false
}

func isShortThreadFollowUp(query string, threadEpisodes []memory.Episode) bool {
	if len(threadEpisodes) == 0 || !hasAssistantTurn(threadEpisodes) {
		return false
	}

	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	if lowerQuery == "" {
		return false
	}

	wordCount := len(strings.FieldsFunc(lowerQuery, func(char rune) bool {
		return unicode.IsSpace(char) || strings.ContainsRune("?!.,;:'\"", char)
	}))

	if wordCount == 0 {
		return false
	}

	return wordCount <= 3 || (strings.HasSuffix(lowerQuery, "?") && len(lowerQuery) <= 24)
}

func hasAssistantTurn(episodes []memory.Episode) bool {
	for _, episode := range episodes {
		if episode.Source == roleAssistant {
			return true
		}
	}

	return false
}

func trimCurrentUserEcho(query string, episodes []memory.Episode) []memory.Episode {
	if len(episodes) == 0 {
		return nil
	}

	last := episodes[len(episodes)-1]
	if last.Source == roleUser && strings.TrimSpace(last.Text) == strings.TrimSpace(query) {
		return append([]memory.Episode(nil), episodes[:len(episodes)-1]...)
	}

	return episodes
}

func renderThreadEpisodes(episodes []memory.Episode) string {
	if len(episodes) == 0 {
		return noContext
	}

	lines := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		text := strings.TrimSpace(episode.Text)
		if text == "" {
			text = episode.Summary
		}
		lines = append(lines, fmt.Sprintf("- [%s] %s", episode.Source, text))
	}

	return strings.Join(lines, "\n")
}
