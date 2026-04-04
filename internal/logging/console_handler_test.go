package logging

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConsoleHandlerHandleWritesFormattedLine(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer
	handler := &consoleHandler{
		level:    slog.LevelInfo,
		writer:   &buffer,
		useColor: false,
		mu:       &sync.Mutex{},
	}

	record := slog.NewRecord(time.Date(2026, 4, 4, 14, 3, 56, 0, time.UTC), slog.LevelInfo, "slack auth succeeded", 0)
	record.AddAttrs(
		slog.String("channel_id", "C123"),
		slog.Group("meta", slog.String("note", "hello world")),
	)

	grouped := handler.WithGroup("ctx").WithAttrs([]slog.Attr{slog.String("bot_id", "B123")})
	if err := grouped.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	output := buffer.String()
	for _, want := range []string{
		"14:03:56",
		"◇ slack",
		"INFO",
		"slack auth succeeded",
		"bot_id=B123",
		"channel_id=C123",
		`ctx.meta.note="hello world"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output %q does not contain %q", output, want)
		}
	}
}

func TestConsoleHandlerSkipsDisabledLevelsAndBlankGroups(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer
	handler := &consoleHandler{
		level:    slog.LevelWarn,
		writer:   &buffer,
		useColor: false,
		mu:       &sync.Mutex{},
	}

	record := slog.NewRecord(time.Date(2026, 4, 4, 14, 3, 56, 0, time.UTC), slog.LevelInfo, "service starting", 0)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if got := buffer.String(); got != "" {
		t.Fatalf("unexpected output: %q", got)
	}

	if returned := handler.WithGroup("   "); returned != handler {
		t.Fatal("blank group should return same handler")
	}
}

func TestConsoleHelpers(t *testing.T) {
	t.Parallel()

	attrs := []slog.Attr{}
	appendResolvedAttr(
		&attrs,
		slog.Group("meta", slog.String("note", "hello world"), slog.Int("count", 2)),
		[]string{"root"},
	)
	if len(attrs) != 2 {
		t.Fatalf("attrs length = %d, want 2", len(attrs))
	}
	if attrs[0].Key != "root.meta.note" || formatAttrValue(attrs[0].Value) != `"hello world"` {
		t.Fatalf("unexpected attr[0]: %+v", attrs[0])
	}
	if attrs[1].Key != "root.meta.count" || formatAttrValue(attrs[1].Value) != "2" {
		t.Fatalf("unexpected attr[1]: %+v", attrs[1])
	}

	if got := renderBody("message", append(attrs, slog.Attr{})); !strings.Contains(got, `root.meta.note="hello world"`) {
		t.Fatalf("renderBody() = %q", got)
	}
	if got := strconvQuote("line 1\n\t\"quoted\""); got != `"line 1\n\t\"quoted\""` {
		t.Fatalf("strconvQuote() = %q", got)
	}
	if got := applyStyle("text", false, ansiRed); got != "text" {
		t.Fatalf("applyStyle() = %q, want %q", got, "text")
	}
	if got := applyStyle("text", true, ansiRed); got != ansiRed+"text"+ansiReset {
		t.Fatalf("applyStyle() = %q", got)
	}
	if got := attrKey("note", nil); got != "note" {
		t.Fatalf("attrKey() = %q, want %q", got, "note")
	}
	if got := attrKey("note", []string{"ctx", "meta"}); got != "ctx.meta.note" {
		t.Fatalf("attrKey() = %q, want %q", got, "ctx.meta.note")
	}
	if got := extendGroups([]string{"ctx"}, ""); len(got) != 1 || got[0] != "ctx" {
		t.Fatalf("extendGroups() with empty key = %#v", got)
	}
	if got := extendGroups([]string{"ctx"}, "meta"); len(got) != 2 || got[1] != "meta" {
		t.Fatalf("extendGroups() with key = %#v", got)
	}
}

func TestConsoleCategoryAndLevelHelpers(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		message  string
		attrs    []slog.Attr
		category string
		icon     string
		colour   string
	}{
		{message: "slack socket connected", category: categorySocket, icon: "⋯", colour: ansiDim},
		{message: "received slack app mention", attrs: []slog.Attr{slog.String("channel_id", "C123")}, category: categorySlack, icon: "◇", colour: ansiYellow},
		{message: "rook service starting", attrs: []slog.Attr{slog.String("chat_model", "qwen3:4b")}, category: categoryModel, icon: "◆", colour: ansiMagenta},
		{message: "memory consolidation complete", category: categoryMemory, icon: "◉", colour: ansiCyan},
		{message: "configuration reload complete", category: categoryApp, icon: "●", colour: ansiBold + ansiBlue},
		{message: "message handling failed", attrs: []slog.Attr{slog.String("error", "boom")}, category: categoryError, icon: "✗", colour: ansiBold + ansiRed},
		{message: "background tick", category: categorySystem, icon: "·", colour: ansiWhite},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.category, func(t *testing.T) {
			t.Parallel()

			if got := classifyCategory(testCase.message, testCase.attrs); got != testCase.category {
				t.Fatalf("classifyCategory() = %q, want %q", got, testCase.category)
			}
			if got := categoryIcon(testCase.category); got != testCase.icon {
				t.Fatalf("categoryIcon() = %q, want %q", got, testCase.icon)
			}
			if got := categoryColor(testCase.category); got != testCase.colour {
				t.Fatalf("categoryColor() = %q, want %q", got, testCase.colour)
			}
		})
	}

	levelCases := []struct {
		level  slog.Level
		label  string
		colour string
	}{
		{level: slog.LevelDebug, label: "DEBUG", colour: ansiDim + ansiCyan},
		{level: slog.LevelInfo, label: "INFO ", colour: ansiGreen},
		{level: slog.LevelWarn, label: "WARN ", colour: ansiYellow},
		{level: slog.LevelError, label: "ERROR", colour: ansiBold + ansiRed},
	}

	for _, testCase := range levelCases {
		if got := levelLabel(testCase.level); got != testCase.label {
			t.Fatalf("levelLabel(%v) = %q, want %q", testCase.level, got, testCase.label)
		}
		if got := levelColor(testCase.level); got != testCase.colour {
			t.Fatalf("levelColor(%v) = %q, want %q", testCase.level, got, testCase.colour)
		}
	}
}

func TestFormatConsoleRecordAndSupportsColor(t *testing.T) {
	record := slog.NewRecord(time.Time{}, slog.LevelInfo, "service starting", 0)
	line := formatConsoleRecord(record, []slog.Attr{slog.String("status", "ok")}, false)
	for _, want := range []string{"app", "INFO", "service starting", "status=ok"} {
		if !strings.Contains(line, want) {
			t.Fatalf("formatConsoleRecord() = %q, missing %q", line, want)
		}
	}

	tempFile, err := os.CreateTemp(t.TempDir(), "rook-log")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	t.Setenv(envNoColor, "")
	t.Setenv("TERM", "xterm-256color")
	if supportsColor(tempFile) {
		t.Fatal("regular file should not support colour")
	}

	deviceFile, err := os.Open("/dev/null")
	if err != nil {
		t.Fatalf("Open(/dev/null) error = %v", err)
	}
	defer func() {
		_ = deviceFile.Close()
	}()

	t.Setenv(envNoColor, "1")
	if supportsColor(deviceFile) {
		t.Fatal("the no-colour env var should disable colour")
	}
	t.Setenv(envNoColor, "")
	t.Setenv("TERM", "dumb")
	if supportsColor(deviceFile) {
		t.Fatal("TERM=dumb should disable colour")
	}
	t.Setenv("TERM", "xterm-256color")
	if !supportsColor(deviceFile) {
		t.Fatal("/dev/null should support colour when env allows it")
	}
}
