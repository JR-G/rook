package agent

import (
	"context"
	"strings"
	"unicode"

	"github.com/JR-G/rook/internal/failures"
	"github.com/JR-G/rook/internal/memory"
	"github.com/JR-G/rook/internal/ollama"
	"github.com/JR-G/rook/internal/output"
)

func (s *Service) repairRepeatedThreadReply(
	ctx context.Context,
	cfg Config,
	systemPrompt string,
	userPrompt string,
	threadEpisodes []memory.Episode,
	reply string,
) (string, error) {
	matchedText := firstMatchingAssistantText(reply, threadEpisodes)
	if matchedText == "" {
		return reply, nil
	}

	messages := buildChatMessages(systemPrompt, userPrompt, threadEpisodes)
	messages = append(messages, ollama.Message{Role: roleAssistant, Content: reply})
	messages = append(messages, ollama.Message{Role: "user", Content: repeatedReplyRepairPrompt(matchedText)})

	result, chatErr := s.chatWithFallback(ctx, cfg, messages, output.AnswerSchema())
	if chatErr != nil {
		return fallbackRepeatedThreadReply(matchedText), nil //nolint:nilerr // intentional fallback on repair failure
	}

	repaired, err := output.ParseAnswer(result.Content)
	if err != nil {
		return "", failures.Wrap(err, "The local model repeated itself and then returned an invalid repair reply.")
	}
	if firstMatchingAssistantText(repaired, threadEpisodes) != "" {
		return fallbackRepeatedThreadReply(matchedText), nil
	}

	return repaired, nil
}

func firstMatchingAssistantText(reply string, episodes []memory.Episode) string {
	fp := replyFingerprint(reply)
	if fp == "" {
		return ""
	}
	replyTokens := strings.Fields(fp)

	for i := len(episodes) - 1; i >= 0; i-- {
		if episodes[i].Source != roleAssistant {
			continue
		}
		text := strings.TrimSpace(episodes[i].Text)
		if text == "" {
			text = strings.TrimSpace(episodes[i].Summary)
		}
		if text == "" {
			continue
		}
		assistantFP := replyFingerprint(text)
		if assistantFP == "" {
			continue
		}
		if fp == assistantFP {
			return text
		}
		assistantTokens := strings.Fields(assistantFP)
		if len(replyTokens) >= 6 && len(assistantTokens) >= 6 && tokenOverlap(replyTokens, assistantTokens) >= 0.70 {
			return text
		}
	}

	return ""
}

func repeatedReplyRepairPrompt(lastAssistant string) string {
	return strings.TrimSpace(
		"Your previous draft repeated the earlier assistant turn instead of answering the latest follow-up.\n" +
			"Write a materially new continuation.\n" +
			"Do not reuse the earlier opening, sentence shape, or metaphor.\n" +
			"Directly address the latest user turn and unpack the previous point.\n" +
			"Earlier assistant turn:\n" + lastAssistant,
	)
}

func fallbackRepeatedThreadReply(lastAssistant string) string {
	if strings.TrimSpace(lastAssistant) == "" {
		return "I repeated myself there. Ask me to unpack one part and I'll take it further."
	}

	return "I repeated myself there. Ask me to unpack the last point and I'll continue it properly."
}

func replyFingerprint(input string) string {
	var builder strings.Builder
	lastSpace := false
	for _, char := range strings.ToLower(strings.TrimSpace(input)) {
		switch {
		case unicode.IsLetter(char) || unicode.IsNumber(char):
			builder.WriteRune(char)
			lastSpace = false
		case unicode.IsSpace(char) || strings.ContainsRune(".,;:!?'-–—()[]{}", char):
			if lastSpace {
				continue
			}
			builder.WriteByte(' ')
			lastSpace = true
		}
	}

	return strings.TrimSpace(builder.String())
}

func tokenOverlap(left, right []string) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}

	leftSet := make(map[string]struct{}, len(left))
	for _, token := range left {
		leftSet[token] = struct{}{}
	}

	rightSet := make(map[string]struct{}, len(right))
	for _, token := range right {
		rightSet[token] = struct{}{}
	}

	intersection := 0
	for token := range leftSet {
		if _, ok := rightSet[token]; ok {
			intersection++
		}
	}

	denominator := len(leftSet)
	if len(rightSet) > denominator {
		denominator = len(rightSet)
	}

	return float64(intersection) / float64(denominator)
}
