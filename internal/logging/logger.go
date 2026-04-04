package logging

import (
	"fmt"
	"log/slog"
	"strings"
)

// New returns a configured structured logger.
func New(level string) (*slog.Logger, error) {
	var parsed slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		parsed = slog.LevelDebug
	case "info", "":
		parsed = slog.LevelInfo
	case "warn", "warning":
		parsed = slog.LevelWarn
	case "error":
		parsed = slog.LevelError
	default:
		return nil, fmt.Errorf("unsupported log level %q", level)
	}

	handler := newConsoleHandler(parsed)

	return slog.New(handler), nil
}
