package commands

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Kind identifies a user-facing command.
type Kind string

// Built-in command kinds.
const (
	KindHelp   Kind = "help"
	KindPing   Kind = "ping"
	KindStatus Kind = "status"
	KindMemory Kind = "memory"
	KindModel  Kind = "model"
	KindReload Kind = "reload"
	KindRemind Kind = "remind"
)

// Command is a parsed message command.
type Command struct {
	Kind Kind
	Args string
}

// ReminderRequest captures a parsed reminder command.
type ReminderRequest struct {
	DueAt   time.Time
	Message string
}

var reminderPattern = regexp.MustCompile(`(?i)^remind(?: me)?\s+(in|at)\s+(.+?)\s+to\s+(.+)$`)

// Parse extracts a built-in command from free text.
func Parse(input string) (Command, bool) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(input, "/"))
	if trimmed == "" {
		return Command{}, false
	}

	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return Command{}, false
	}

	kind := Kind(strings.ToLower(fields[0]))
	switch kind {
	case KindHelp, KindPing, KindStatus, KindMemory, KindModel, KindReload, KindRemind:
	default:
		return Command{}, false
	}

	args := strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))

	return Command{Kind: kind, Args: args}, true
}

// ParseReminder parses built-in reminder syntax.
func ParseReminder(now time.Time, location *time.Location, input string) (ReminderRequest, bool, error) {
	matches := reminderPattern.FindStringSubmatch(strings.TrimSpace(input))
	if len(matches) == 0 {
		return ReminderRequest{}, false, nil
	}

	whenKind := strings.ToLower(strings.TrimSpace(matches[1]))
	whenExpr := strings.TrimSpace(matches[2])
	message := strings.TrimSpace(matches[3])
	if message == "" {
		return ReminderRequest{}, true, fmt.Errorf("reminder text must not be empty")
	}

	var dueAt time.Time
	switch whenKind {
	case "in":
		duration, err := parseRelativeDuration(whenExpr)
		if err != nil {
			return ReminderRequest{}, true, fmt.Errorf("invalid reminder duration: %w", err)
		}
		dueAt = now.Add(duration)
	case "at":
		parsed, err := parseAbsoluteTime(location, whenExpr)
		if err != nil {
			return ReminderRequest{}, true, fmt.Errorf("invalid reminder time: %w", err)
		}
		dueAt = parsed
	default:
		return ReminderRequest{}, true, fmt.Errorf("unsupported reminder mode %q", whenKind)
	}

	if !dueAt.After(now) {
		return ReminderRequest{}, true, fmt.Errorf("reminder time must be in the future")
	}

	return ReminderRequest{
		DueAt:   dueAt,
		Message: message,
	}, true, nil
}

func parseRelativeDuration(input string) (time.Duration, error) {
	normalised := strings.TrimSpace(strings.ToLower(input))
	if !strings.HasSuffix(normalised, "d") {
		return time.ParseDuration(normalised)
	}

	value, err := strconv.Atoi(strings.TrimSuffix(normalised, "d"))
	if err != nil {
		return 0, err
	}

	return time.Duration(value) * 24 * time.Hour, nil
}

func parseAbsoluteTime(location *time.Location, input string) (time.Time, error) {
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
	}

	for _, layout := range layouts {
		parsed, err := parseAtLayout(location, layout, input)
		if err == nil {
			return parsed, nil
		}
	}

	return time.Time{}, fmt.Errorf("expected RFC3339 or YYYY-MM-DD HH:MM")
}

func parseAtLayout(location *time.Location, layout, input string) (time.Time, error) {
	if layout == time.RFC3339 {
		return time.Parse(layout, input)
	}

	return time.ParseInLocation(layout, input, location)
}
