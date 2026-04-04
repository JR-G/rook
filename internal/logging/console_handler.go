package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	ansiReset   = "\033[0m"
	ansiDim     = "\033[2m"
	ansiRed     = "\033[31m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiBlue    = "\033[34m"
	ansiMagenta = "\033[35m"
	ansiCyan    = "\033[36m"
	ansiWhite   = "\033[37m"
	ansiBold    = "\033[1m"

	categoryApp    = "app"
	categorySocket = "socket"
	categorySlack  = "slack"
	categoryModel  = "model"
	categoryMemory = "memory"
	categoryError  = "error"
	categorySystem = "system"

	envNoColor = "NO_" + "COL" + "OR"
)

type consoleHandler struct {
	level    slog.Level
	writer   io.Writer
	useColor bool
	attrs    []slog.Attr
	groups   []string
	mu       *sync.Mutex
}

func newConsoleHandler(level slog.Level) slog.Handler {
	return &consoleHandler{
		level:    level,
		writer:   os.Stdout,
		useColor: supportsColor(os.Stdout),
		mu:       &sync.Mutex{},
	}
}

func (handler *consoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= handler.level
}

func (handler *consoleHandler) Handle(_ context.Context, record slog.Record) error {
	if !handler.Enabled(context.Background(), record.Level) {
		return nil
	}

	attrs := make([]slog.Attr, 0, len(handler.attrs)+record.NumAttrs())
	attrs = append(attrs, handler.attrs...)
	record.Attrs(func(attr slog.Attr) bool {
		appendResolvedAttr(&attrs, attr, handler.groups)
		return true
	})

	line := formatConsoleRecord(record, attrs, handler.useColor)

	handler.mu.Lock()
	defer handler.mu.Unlock()
	_, err := io.WriteString(handler.writer, line)
	return err
}

func (handler *consoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(handler.attrs)+len(attrs))
	merged = append(merged, handler.attrs...)
	for _, attr := range attrs {
		appendResolvedAttr(&merged, attr, handler.groups)
	}

	return &consoleHandler{
		level:    handler.level,
		writer:   handler.writer,
		useColor: handler.useColor,
		attrs:    merged,
		groups:   append([]string(nil), handler.groups...),
		mu:       handler.mu,
	}
}

func (handler *consoleHandler) WithGroup(name string) slog.Handler {
	if strings.TrimSpace(name) == "" {
		return handler
	}

	return &consoleHandler{
		level:    handler.level,
		writer:   handler.writer,
		useColor: handler.useColor,
		attrs:    append([]slog.Attr(nil), handler.attrs...),
		groups:   append(append([]string(nil), handler.groups...), name),
		mu:       handler.mu,
	}
}

func formatConsoleRecord(record slog.Record, attrs []slog.Attr, useColor bool) string {
	timestamp := record.Time
	if timestamp.IsZero() {
		timestamp = time.Now()
	}

	category := classifyCategory(record.Message, attrs)
	body := renderBody(record.Message, attrs)
	timeText := applyStyle(timestamp.Format("15:04:05"), useColor, ansiDim)
	categoryText := fmt.Sprintf("%s %-8s", categoryIcon(category), category)
	categoryText = applyStyle(categoryText, useColor, categoryColor(category))
	levelText := applyStyle(levelLabel(record.Level), useColor, levelColor(record.Level))

	return fmt.Sprintf("%s %s %s %s\n", timeText, categoryText, levelText, body)
}

func appendResolvedAttr(attrs *[]slog.Attr, attr slog.Attr, groups []string) {
	resolved := attr.Value.Resolve()
	if resolved.Kind() != slog.KindGroup {
		*attrs = append(*attrs, slog.Attr{
			Key:   attrKey(attr.Key, groups),
			Value: resolved,
		})
		return
	}

	appendGroupAttrs(attrs, attr.Key, resolved.Group(), groups)
}

func appendGroupAttrs(attrs *[]slog.Attr, key string, children []slog.Attr, groups []string) {
	nextGroups := extendGroups(groups, key)
	for _, child := range children {
		appendResolvedAttr(attrs, child, nextGroups)
	}
}

func attrKey(key string, groups []string) string {
	if len(groups) == 0 {
		return key
	}

	return strings.Join(append(append([]string(nil), groups...), key), ".")
}

func extendGroups(groups []string, key string) []string {
	if key == "" {
		return groups
	}

	return append(append([]string(nil), groups...), key)
}

func renderBody(message string, attrs []slog.Attr) string {
	parts := []string{message}
	for _, attr := range attrs {
		if strings.TrimSpace(attr.Key) == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", attr.Key, formatAttrValue(attr.Value)))
	}

	return strings.Join(parts, " ")
}

func formatAttrValue(value slog.Value) string {
	switch value.Kind() {
	case slog.KindString:
		text := value.String()
		if strings.ContainsAny(text, " \t\n\"") {
			return strconvQuote(text)
		}
		return text
	case slog.KindAny,
		slog.KindBool,
		slog.KindDuration,
		slog.KindFloat64,
		slog.KindGroup,
		slog.KindInt64,
		slog.KindLogValuer,
		slog.KindTime,
		slog.KindUint64:
		return fmt.Sprint(value.Any())
	}

	return fmt.Sprint(value.Any())
}

func strconvQuote(text string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + replacer.Replace(text) + `"`
}

func classifyCategory(message string, attrs []slog.Attr) string {
	lowerMessage := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.Contains(lowerMessage, "slack socket"), strings.Contains(lowerMessage, "events api"), strings.Contains(lowerMessage, "socket"):
		return categorySocket
	case strings.Contains(lowerMessage, "slack "), strings.Contains(lowerMessage, "channel"), hasAttr(attrs, "channel_id"):
		return categorySlack
	case strings.Contains(lowerMessage, "model"), strings.Contains(lowerMessage, "ollama"), hasAttr(attrs, "chat_model"), hasAttr(attrs, "embedding_model"):
		return categoryModel
	case strings.Contains(lowerMessage, "memory"), hasAttr(attrs, "memory_count"):
		return categoryMemory
	case strings.Contains(lowerMessage, "reload"), strings.Contains(lowerMessage, "status"), strings.Contains(lowerMessage, "service starting"):
		return categoryApp
	case strings.Contains(lowerMessage, "error"), hasAttr(attrs, "error"):
		return categoryError
	default:
		return categorySystem
	}
}

func hasAttr(attrs []slog.Attr, key string) bool {
	for _, attr := range attrs {
		if attr.Key == key {
			return true
		}
	}

	return false
}

func categoryIcon(category string) string {
	switch category {
	case categoryApp:
		return "●"
	case categorySocket:
		return "⋯"
	case categorySlack:
		return "◇"
	case categoryModel:
		return "◆"
	case categoryMemory:
		return "◉"
	case categoryError:
		return "✗"
	default:
		return "·"
	}
}

func categoryColor(category string) string {
	switch category {
	case categoryApp:
		return ansiBold + ansiBlue
	case categorySocket:
		return ansiDim
	case categorySlack:
		return ansiYellow
	case categoryModel:
		return ansiMagenta
	case categoryMemory:
		return ansiCyan
	case categoryError:
		return ansiBold + ansiRed
	default:
		return ansiWhite
	}
}

func levelLabel(level slog.Level) string {
	switch {
	case level <= slog.LevelDebug:
		return "DEBUG"
	case level < slog.LevelWarn:
		return "INFO "
	case level < slog.LevelError:
		return "WARN "
	default:
		return "ERROR"
	}
}

func levelColor(level slog.Level) string {
	switch {
	case level <= slog.LevelDebug:
		return ansiDim + ansiCyan
	case level < slog.LevelWarn:
		return ansiGreen
	case level < slog.LevelError:
		return ansiYellow
	default:
		return ansiBold + ansiRed
	}
}

func applyStyle(text string, useColor bool, style string) string {
	if !useColor {
		return text
	}

	return style + text + ansiReset
}

func supportsColor(file *os.File) bool {
	if os.Getenv(envNoColor) != "" || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}

	info, err := file.Stat()
	if err != nil {
		return false
	}

	return (info.Mode() & os.ModeCharDevice) != 0
}
