package output

import (
	"encoding/json"
	"errors"
	"strings"
)

// ErrInvalidStructuredAnswer reports that a model reply did not satisfy the
// answer schema expected by the Slack agent.
var ErrInvalidStructuredAnswer = errors.New("invalid structured answer")

// AnswerSchema returns the JSON schema enforced for user-visible replies.
func AnswerSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"answer": map[string]any{
				"type": "string",
			},
		},
		"required":             []string{"answer"},
		"additionalProperties": false,
	}
}

// AnswerSchemaString returns the reply schema as compact JSON for prompt grounding.
func AnswerSchemaString() string {
	payload, err := json.Marshal(AnswerSchema())
	if err != nil {
		return `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`
	}

	return string(payload)
}

// ParseAnswer extracts the validated answer field from a structured model reply.
func ParseAnswer(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if !json.Valid([]byte(trimmed)) {
		return "", ErrInvalidStructuredAnswer
	}

	var payload struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return "", ErrInvalidStructuredAnswer
	}

	answer := normaliseAnswer(payload.Answer)
	if answer == "" {
		return "", ErrInvalidStructuredAnswer
	}

	return answer, nil
}

func normaliseAnswer(input string) string {
	cleaned := thinkingBlockPattern.ReplaceAllString(strings.TrimSpace(input), "")
	if finalReply, ok := extractFinalReply(cleaned); ok {
		cleaned = finalReply
	}

	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.ReplaceAll(cleaned, "<final>", "")
	cleaned = strings.ReplaceAll(cleaned, "</final>", "")
	cleaned = blankLinePattern.ReplaceAllString(strings.TrimSpace(cleaned), "\n\n")

	return strings.TrimSpace(cleaned)
}
