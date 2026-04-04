package output

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	thinkingBlockPattern = regexp.MustCompile(`(?is)<think>.*?</think>`)
	blankLinePattern     = regexp.MustCompile(`\n{3,}`)
	finalBlockPattern    = regexp.MustCompile(`(?is)<final>\s*(.*?)\s*</final>`)
)

const (
	invalidStructuredReply = "I generated internal output instead of a user-facing reply. Please try again."
	openFinalTag           = "<final>"
)

// Filter sanitises model output before it is shown in Slack.
type Filter struct {
	MaxChars int
}

// New returns a filter with a Slack-safe default limit.
func New() Filter {
	return Filter{MaxChars: 2800}
}

// Clean removes internal-only noise and truncates oversized output.
func (f Filter) Clean(input string) string {
	cleaned := strings.TrimSpace(input)
	if cleaned == "" {
		return "I don't have a clean reply yet. Please try again."
	}

	cleaned = thinkingBlockPattern.ReplaceAllString(cleaned, "")
	structuredReply, ok := extractStructuredReply(cleaned)
	if !ok {
		return invalidStructuredReply
	}
	cleaned = structuredReply
	cleaned = removeInternalLines(cleaned)
	cleaned = blankLinePattern.ReplaceAllString(strings.TrimSpace(cleaned), "\n\n")

	if cleaned == "" {
		return invalidStructuredReply
	}

	if f.MaxChars > 0 && len(cleaned) > f.MaxChars {
		cleaned = strings.TrimSpace(cleaned[:f.MaxChars-1]) + "…"
	}

	return cleaned
}

func extractStructuredReply(input string) (string, bool) {
	if finalReply, ok := extractFinalBlock(input); ok {
		return finalReply, true
	}

	jsonReply := extractPrimaryText(input)
	if jsonReply == "" {
		return "", false
	}

	return unwrapStructuredText(jsonReply)
}

func extractFinalBlock(input string) (string, bool) {
	matches := finalBlockPattern.FindStringSubmatch(input)
	if len(matches) != 2 {
		return "", false
	}

	return strings.TrimSpace(matches[1]), true
}

func extractPrimaryText(input string) string {
	trimmed := strings.TrimSpace(input)
	if !json.Valid([]byte(trimmed)) {
		return ""
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return trimmed
	}

	for _, key := range []string{"answer", "response", "content", "text", "message"} {
		value, ok := payload[key]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if ok && strings.TrimSpace(text) != "" {
			return text
		}
	}

	return ""
}

func unwrapStructuredText(input string) (string, bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", false
	}

	if finalReply, ok := extractFinalBlock(trimmed); ok {
		return finalReply, true
	}

	lowerTrimmed := strings.ToLower(trimmed)
	for _, prefix := range []string{"block:", "reply:", "final:"} {
		if !strings.HasPrefix(lowerTrimmed, prefix) {
			continue
		}
		trimmed = strings.TrimSpace(trimmed[len(prefix):])
		break
	}

	if trimmed == "" {
		return "", false
	}
	if finalReply, ok := extractFinalBlock(trimmed); ok {
		return finalReply, true
	}
	if finalReply, ok := extractOpenFinalRemainder(trimmed); ok {
		return finalReply, true
	}

	return trimmed, true
}

func extractOpenFinalRemainder(input string) (string, bool) {
	lowerInput := strings.ToLower(input)
	index := strings.Index(lowerInput, openFinalTag)
	if index < 0 {
		return "", false
	}

	remainder := strings.TrimSpace(input[index+len(openFinalTag):])
	remainder = strings.TrimSuffix(remainder, "</final>")
	remainder = strings.TrimSpace(remainder)
	if remainder == "" {
		return "", false
	}

	return remainder, true
}

func removeInternalLines(input string) string {
	lines := strings.Split(input, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			kept = append(kept, "")
			continue
		}

		lower := strings.ToLower(trimmed)
		switch {
		case strings.HasPrefix(lower, "tool:"),
			strings.HasPrefix(lower, "tool result:"),
			strings.HasPrefix(lower, "function call:"),
			strings.HasPrefix(lower, "provider payload:"),
			strings.HasPrefix(lower, "internal note:"),
			strings.HasPrefix(lower, "analysis:"),
			strings.HasPrefix(lower, "user request:"),
			strings.HasPrefix(lower, "relevant memory:"),
			strings.HasPrefix(lower, "user facts:"),
			strings.HasPrefix(lower, "working context:"),
			strings.HasPrefix(lower, "historical episodes:"),
			strings.HasPrefix(lower, "live web results:"),
			strings.HasPrefix(lower, "recent squad0 context:"),
			strings.HasPrefix(lower, "core constitution:"),
			strings.HasPrefix(lower, "stable identity:"),
			strings.HasPrefix(lower, "evolving voice:"),
			strings.HasPrefix(lower, "slack output rules:"):
			continue
		}

		kept = append(kept, line)
	}

	return strings.Join(kept, "\n")
}
