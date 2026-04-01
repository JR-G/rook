package output

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	thinkingBlockPattern = regexp.MustCompile(`(?is)<think>.*?</think>`)
	blankLinePattern     = regexp.MustCompile(`\n{3,}`)
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
	cleaned = extractPrimaryText(cleaned)
	cleaned = removeInternalLines(cleaned)
	cleaned = blankLinePattern.ReplaceAllString(strings.TrimSpace(cleaned), "\n\n")

	if cleaned == "" {
		return "I generated internal output instead of a user-facing reply. Please try again."
	}

	if f.MaxChars > 0 && len(cleaned) > f.MaxChars {
		cleaned = strings.TrimSpace(cleaned[:f.MaxChars-1]) + "…"
	}

	return cleaned
}

func extractPrimaryText(input string) string {
	trimmed := strings.TrimSpace(input)
	if !json.Valid([]byte(trimmed)) {
		return trimmed
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

	return trimmed
}

func removeInternalLines(input string) string {
	var kept []string
	for _, line := range strings.Split(input, "\n") {
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
			strings.HasPrefix(lower, "analysis:"):
			continue
		}

		kept = append(kept, line)
	}

	return strings.Join(kept, "\n")
}
