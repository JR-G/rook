package failures

import "errors"

const defaultInternalMessage = "I hit an internal error handling that message."

// UserVisible describes an error that can safely present a short message to the user.
type UserVisible interface {
	error
	UserMessage() string
}

type userVisibleError struct {
	cause   error
	message string
}

// Error implements error.
func (err userVisibleError) Error() string {
	if err.cause == nil {
		return err.message
	}

	return err.cause.Error()
}

// Unwrap exposes the underlying cause.
func (err userVisibleError) Unwrap() error {
	return err.cause
}

// UserMessage returns the Slack-safe error string.
func (err userVisibleError) UserMessage() string {
	return err.message
}

// Wrap returns an error carrying a user-visible message.
func Wrap(cause error, message string) error {
	if cause == nil {
		return nil
	}

	var visible UserVisible
	if errors.As(cause, &visible) {
		return cause
	}

	return userVisibleError{
		cause:   cause,
		message: message,
	}
}

// Message extracts a user-visible message when available.
func Message(err error) string {
	var visible UserVisible
	if errors.As(err, &visible) {
		return visible.UserMessage()
	}

	return ""
}

// MessageOr extracts a user-visible message or falls back to a generic one.
func MessageOr(err error) string {
	message := Message(err)
	if message != "" {
		return message
	}

	return defaultInternalMessage
}
