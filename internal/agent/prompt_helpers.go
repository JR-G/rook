package agent

import (
	"strings"

	"github.com/JR-G/rook/internal/memory"
)

func adjustRetrievalForQuery(
	channelID string,
	threadTS string,
	threadEpisodes []memory.Episode,
	retrieval memory.RetrievalContext,
) memory.RetrievalContext {
	retrieval.Episodes = excludeThreadEpisodes(retrieval.Episodes, channelID, threadTS)
	if len(threadEpisodes) > 0 {
		retrieval.Episodes = nil
	}

	return retrieval
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
