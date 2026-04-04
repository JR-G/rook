package ollama

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/JR-G/rook/internal/failures"
)

var quotedFieldPattern = regexp.MustCompile(`\b(status|msg)="([^"]+)"`)

// WrapUserVisible wraps an Ollama/runtime failure with a Slack-safe message.
func WrapUserVisible(err error) error {
	return failures.Wrap(err, userFacingMessage(err))
}

func userFacingMessage(err error) string {
	if err == nil {
		return ""
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return "The local model took too long to reply. It may still be warming up. Try again in a minute."
	}
	if errors.Is(err, context.Canceled) {
		return "That request stopped before the local model finished."
	}

	var statusErr StatusError
	if !errors.As(err, &statusErr) {
		return ""
	}

	lowerMessage := strings.ToLower(strings.TrimSpace(statusErr.Message))
	switch {
	case strings.Contains(lowerMessage, "loading model"),
		strings.Contains(lowerMessage, "waiting for server"),
		strings.Contains(lowerMessage, "runner"):
		return "The local model is still warming up. Try again in a minute."
	case statusErr.StatusCode == http.StatusTooManyRequests:
		return "The local model is busy right now. Try again in a moment."
	case statusErr.StatusCode >= http.StatusInternalServerError:
		return "The local model failed while generating a reply. Try again in a moment."
	default:
		return ""
	}
}

func sanitiseStatusMessage(body string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	statusMessages := make([]string, 0, len(lines))
	messageFields := make([]string, 0, len(lines))
	fallbackLines := make([]string, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "[GIN]") {
			continue
		}

		matches := quotedFieldPattern.FindAllStringSubmatch(trimmed, -1)
		for _, match := range matches {
			switch match[1] {
			case "status":
				statusMessages = append(statusMessages, match[2])
			case "msg":
				messageFields = append(messageFields, match[2])
			}
		}

		if !strings.Contains(trimmed, "=") {
			fallbackLines = append(fallbackLines, trimmed)
		}
	}

	switch {
	case len(statusMessages) > 0:
		return statusMessages[len(statusMessages)-1]
	case len(messageFields) > 0:
		return messageFields[len(messageFields)-1]
	case len(fallbackLines) > 0:
		return fallbackLines[0]
	default:
		return strings.TrimSpace(body)
	}
}
