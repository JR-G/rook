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

const (
	invalidStructuredReply = "I generated internal output instead of a user-facing reply. Please try again."
	openFinalTag           = "<final>"
)

// Filter is a fallback sanitizer for legacy or malformed model output.
// The main runtime path now relies on schema-constrained replies instead.
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
	if jsonReply, ok := extractJSONStructuredReply(input); ok {
		return jsonReply, true
	}

	return extractFinalReply(input)
}

func extractJSONStructuredReply(input string) (string, bool) {
	jsonReply := extractPrimaryText(input)
	if jsonReply == "" {
		return "", false
	}
	if finalReply, ok := extractFinalReply(jsonReply); ok {
		return finalReply, true
	}

	return strings.TrimSpace(jsonReply), strings.TrimSpace(jsonReply) != ""
}

func extractFinalReply(input string) (string, bool) {
	lowerInput := strings.ToLower(input)
	openIndex := strings.LastIndex(lowerInput, openFinalTag)
	if openIndex < 0 {
		return "", false
	}

	remainder := input[openIndex+len(openFinalTag):]
	closeIndex := strings.Index(strings.ToLower(remainder), "</final>")
	if closeIndex >= 0 {
		remainder = remainder[:closeIndex]
	}

	trimmed := strings.TrimSpace(remainder)
	if trimmed == "" {
		return "", false
	}

	return trimmed, true
}

func extractOpenFinalReply(input string) (string, bool) {
	return extractFinalReply(input)
}

func extractPrimaryText(input string) string {
	trimmed := strings.TrimSpace(input)
	if !json.Valid([]byte(trimmed)) {
		return ""
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return ""
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
			strings.HasPrefix(lower, "slack output rules:"),
			trimmed == "<final>" || trimmed == "</final>":
			continue
		}

		kept = append(kept, line)
	}

	return strings.Join(kept, "\n")
}
