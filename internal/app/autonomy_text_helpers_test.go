package app

import (
	"strings"
	"testing"
	"time"

	"github.com/JR-G/rook/internal/memory"
)

const jam24Ref = "JAM-24"

func TestWeeknoteTextHelpers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 10, 12, 0, 0, 0, time.UTC)
	episodes := []memory.Episode{
		{Source: sourceAmbientAgent, ChannelID: "C1", UserID: "B1", Summary: "OPS-9 shipped cleanly", CreatedAt: now.Add(-3 * time.Minute)},
		{Source: sourceAmbientAgent, ChannelID: "C1", UserID: "B1", Summary: "OPS-9 follow-up landed via PR #7", CreatedAt: now.Add(-2 * time.Minute)},
		{Source: sourceAmbientAgent, ChannelID: "C2", UserID: "B2", Summary: jam24Ref + " blocked for reassignment", CreatedAt: now.Add(-time.Minute)},
		{Source: sourceAmbientAgent, ChannelID: "C2", UserID: "B2", Summary: jam24Ref + " re-review still open. PR #26.", CreatedAt: now},
	}

	if got := weeknoteThemeLines(nil); len(got) != 1 || !strings.Contains(got[0], "none clearly dominated") {
		t.Fatalf("unexpected empty weeknote theme lines %#v", got)
	}
	if got := reflectionThemeLines(nil); len(got) != 1 || !strings.Contains(got[0], "none obvious") {
		t.Fatalf("unexpected empty reflection theme lines %#v", got)
	}
	if got := compactWeeknoteTextLimit("keep me", 0); got != "keep me" {
		t.Fatalf("unexpected unlimited compact text %q", got)
	}
	if refs := extractWeeknoteRefs(""); refs != nil {
		t.Fatalf("expected nil refs for empty text, got %#v", refs)
	}
	refs := extractWeeknoteRefs(jam24Ref + " and " + jam24Ref + " plus pr #26")
	if len(refs) != 2 || refs[0] != jam24Ref || refs[1] != "PR #26" {
		t.Fatalf("unexpected extracted refs %#v", refs)
	}
	if !weeknoteRefsOverlap([]string{jam24Ref}, map[string]struct{}{jam24Ref: {}}) {
		t.Fatal("expected overlapping refs to match")
	}
	if weeknoteRefsOverlap([]string{"OPS-9"}, map[string]struct{}{jam24Ref: {}}) {
		t.Fatal("did not expect non-overlapping refs to match")
	}

	themes := weeknoteThemes(episodes)
	if len(themes) != 2 {
		t.Fatalf("expected two repeated themes, got %#v", themes)
	}
	if themes[0].Label != jam24Ref || themes[1].Label != "OPS-9" {
		t.Fatalf("unexpected theme order %#v", themes)
	}

	blockerOnly := renderWeeknoteTheme(weeknoteTheme{
		Label:          "BUG-1",
		Mentions:       2,
		Latest:         "BUG-1 stuck waiting on reassignment",
		BlockerSignals: 1,
	})
	if !strings.Contains(blockerOnly, "resurfacing as friction") {
		t.Fatalf("unexpected blocker-only theme %q", blockerOnly)
	}

	defaultTheme := renderWeeknoteTheme(weeknoteTheme{
		Label:    "OPS-9",
		Mentions: 2,
		Latest:   "OPS-9 follow-up landed via PR #7",
	})
	if !strings.Contains(defaultTheme, "was the main thread") {
		t.Fatalf("unexpected default theme %q", defaultTheme)
	}

	cues := formatReflectionCues(episodes)
	if !strings.Contains(cues, "Source mix:") || !strings.Contains(cues, "Concrete moment:") {
		t.Fatalf("unexpected reflection cues %q", cues)
	}

	lowSignal := fallbackWeeknoteText([]memory.Episode{{ChannelID: "C1", UserID: "B1"}})
	if !strings.Contains(lowSignal, "Mostly coordination, nudges, and follow-through.") {
		t.Fatalf("expected low-signal fallback text, got %q", lowSignal)
	}
}

func TestWeeknoteTextHelperAdditionalBranches(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 10, 12, 0, 0, 0, time.UTC)
	themeLines := weeknoteThemeLines([]weeknoteTheme{
		{Label: "OPS-9", Mentions: 2, Latest: "OPS-9 follow-up landed cleanly"},
	})
	if len(themeLines) != 1 || !strings.Contains(themeLines[0], "OPS-9") {
		t.Fatalf("unexpected non-empty weeknote theme lines %#v", themeLines)
	}

	reflectionLines := reflectionThemeLines([]weeknoteTheme{
		{Label: jam24Ref, Mentions: 2, Latest: jam24Ref + " blocked again", BlockerSignals: 1},
	})
	if len(reflectionLines) != 1 || !strings.Contains(reflectionLines[0], jam24Ref) {
		t.Fatalf("unexpected non-empty reflection theme lines %#v", reflectionLines)
	}

	if got := formatReflectionCues(nil); got != "- Quiet window." {
		t.Fatalf("unexpected empty reflection cues %q", got)
	}
	if got := formatReflectionOpenings(nil); got != "- none recorded" {
		t.Fatalf("unexpected empty reflection openings %q", got)
	}
	if got := reflectionOpening("one two three four five six seven"); got != "one two three four five six" {
		t.Fatalf("unexpected reflection opening %q", got)
	}

	episodes := []memory.Episode{
		{Source: sourceAmbientAgent, Summary: "DOC-3 drafted", CreatedAt: now.Add(-5 * time.Minute)},
		{Source: sourceAmbientAgent, Summary: "DOC-3 revised", CreatedAt: now.Add(-4 * time.Minute)},
		{Source: sourceAmbientAgent, Summary: "OPS-9 shipped", CreatedAt: now.Add(-3 * time.Minute)},
		{Source: sourceAmbientAgent, Summary: "OPS-9 follow-up merged", CreatedAt: now.Add(-2 * time.Minute)},
		{Source: sourceAmbientAgent, Summary: jam24Ref + " blocked for reassignment", CreatedAt: now.Add(-time.Minute)},
		{Source: sourceAmbientAgent, Summary: jam24Ref + " re-review still open. PR #26.", CreatedAt: now},
	}
	themes := weeknoteThemes(episodes)
	if len(themes) != weeknoteThemeLimit {
		t.Fatalf("expected theme limit %d, got %#v", weeknoteThemeLimit, themes)
	}
	if themes[0].Label != jam24Ref || themes[1].Label != "OPS-9" {
		t.Fatalf("unexpected limited theme order %#v", themes)
	}

	openings := formatReflectionOpenings([]memory.Episode{
		{Source: sourceReflection, Text: "The seam is moving under the floorboards again."},
		{Source: sourceReflection, Text: ""},
	})
	if !strings.Contains(openings, "The seam is moving under") {
		t.Fatalf("unexpected reflection openings %q", openings)
	}
}
