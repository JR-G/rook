package app

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/JR-G/rook/internal/memory"
)

const (
	weeknoteHighlightLimit = 3
	weeknoteThemeLimit     = 2
)

var (
	weeknoteIssueKeyPattern = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b`)
	weeknotePRPattern       = regexp.MustCompile(`(?i)\bPR\s*#\d+\b`)
)

type weeknoteTheme struct {
	Label          string
	Mentions       int
	Latest         string
	LastIndex      int
	BlockerSignals int
	ReviewSignals  int
}

type weeknoteRollup struct {
	UpdateCount  int
	ChannelCount int
	ActorCount   int
	Themes       []weeknoteTheme
	Highlights   []string
}

func fallbackWeeknoteText(episodes []memory.Episode) string {
	if len(episodes) == 0 {
		return "📭 Quiet week. No agent activity worth surfacing, so I'm leaving the channel clean."
	}

	rollup := buildWeeknoteRollup(episodes)

	var builder strings.Builder
	builder.WriteString("📣 *Rook weeknote*\n")
	builder.WriteString(fmt.Sprintf("Not a quiet week: observed %d agent updates", rollup.UpdateCount))
	if count := rollup.ChannelCount; count > 0 {
		builder.WriteString(fmt.Sprintf(" across %d %s", count, pluralizeWeeknote("channel", count)))
	}
	if count := rollup.ActorCount; count > 0 {
		builder.WriteString(fmt.Sprintf(" from %d %s", count, pluralizeWeeknote("agent", count)))
	}
	builder.WriteString(".")
	if rollup.ActorCount == 1 && rollup.UpdateCount >= 6 {
		builder.WriteString(" One agent carried all of that traffic.")
	}
	if len(rollup.Themes) == 0 && len(rollup.Highlights) == 0 {
		builder.WriteString(" Mostly coordination, nudges, and follow-through.")
		return builder.String()
	}

	if len(rollup.Themes) > 0 {
		builder.WriteString("\n\n*🔥 Main threads*")
		for _, theme := range rollup.Themes {
			builder.WriteString("\n- ")
			builder.WriteString(renderWeeknoteTheme(theme))
		}
	}

	if len(rollup.Highlights) > 0 {
		builder.WriteString("\n\n*👀 Latest turns*")
		for _, highlight := range rollup.Highlights {
			builder.WriteString("\n- ")
			builder.WriteString(highlight)
		}
	}

	return builder.String()
}

func buildWeeknoteRollup(episodes []memory.Episode) weeknoteRollup {
	channelIDs := make(map[string]struct{}, len(episodes))
	actorIDs := make(map[string]struct{}, len(episodes))
	for _, episode := range episodes {
		if channelID := strings.TrimSpace(episode.ChannelID); channelID != "" {
			channelIDs[channelID] = struct{}{}
		}
		if actorID := strings.TrimSpace(episode.UserID); actorID != "" {
			actorIDs[actorID] = struct{}{}
		}
	}

	themes := weeknoteThemes(episodes)
	consumedRefs := make(map[string]struct{}, len(themes))
	for _, theme := range themes {
		consumedRefs[theme.Label] = struct{}{}
	}

	return weeknoteRollup{
		UpdateCount:  len(episodes),
		ChannelCount: len(channelIDs),
		ActorCount:   len(actorIDs),
		Themes:       themes,
		Highlights:   weeknoteHighlights(episodes, consumedRefs),
	}
}

func formatWeeknoteRollup(rollup weeknoteRollup) string {
	if rollup.UpdateCount == 0 {
		return "- Quiet week."
	}

	lines := []string{
		fmt.Sprintf(
			"- Totals: %d updates, %d %s, %d %s.",
			rollup.UpdateCount,
			rollup.ChannelCount,
			pluralizeWeeknote("channel", rollup.ChannelCount),
			rollup.ActorCount,
			pluralizeWeeknote("agent", rollup.ActorCount),
		),
	}
	lines = append(lines, weeknoteThemeLines(rollup.Themes)...)

	for _, highlight := range rollup.Highlights {
		lines = append(lines, "- Latest signal: "+highlight)
	}

	return strings.Join(lines, "\n")
}

func weeknoteThemeLines(themes []weeknoteTheme) []string {
	if len(themes) == 0 {
		return []string{"- Repeating thread: none clearly dominated."}
	}

	lines := make([]string, 0, len(themes))
	for _, theme := range themes {
		lines = append(lines, "- Repeating thread: "+renderWeeknoteTheme(theme))
	}

	return lines
}

func weeknoteHighlights(episodes []memory.Episode, consumedRefs map[string]struct{}) []string {
	highlights := make([]string, 0, weeknoteHighlightLimit)
	seen := make(map[string]struct{}, weeknoteHighlightLimit)

	for index := len(episodes) - 1; index >= 0 && len(highlights) < weeknoteHighlightLimit; index-- {
		summary := weeknoteEpisodeText(episodes[index])
		if summary == "" {
			continue
		}
		refs := extractWeeknoteRefs(summary)
		if weeknoteRefsOverlap(refs, consumedRefs) {
			continue
		}

		key := weeknoteHighlightKey(summary, refs)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		highlights = append(highlights, summary)
	}

	return highlights
}

func weeknoteThemes(episodes []memory.Episode) []weeknoteTheme {
	byLabel := make(map[string]*weeknoteTheme)

	for index, episode := range episodes {
		summary := weeknoteEpisodeText(episode)
		if summary == "" {
			continue
		}

		refs := extractWeeknoteRefs(summary)
		if len(refs) == 0 {
			continue
		}

		lower := strings.ToLower(summary)
		for _, ref := range refs {
			theme, ok := byLabel[ref]
			if !ok {
				theme = &weeknoteTheme{Label: ref}
				byLabel[ref] = theme
			}
			theme.Mentions++
			theme.Latest = summary
			theme.LastIndex = index
			if weeknoteHasBlockerSignal(lower) {
				theme.BlockerSignals++
			}
			if weeknoteHasReviewSignal(lower) {
				theme.ReviewSignals++
			}
		}
	}

	themes := make([]weeknoteTheme, 0, len(byLabel))
	for _, theme := range byLabel {
		if theme.Mentions < 2 {
			continue
		}
		themes = append(themes, *theme)
	}

	sort.Slice(themes, func(left, right int) bool {
		if themes[left].Mentions != themes[right].Mentions {
			return themes[left].Mentions > themes[right].Mentions
		}
		if themes[left].BlockerSignals != themes[right].BlockerSignals {
			return themes[left].BlockerSignals > themes[right].BlockerSignals
		}
		if themes[left].ReviewSignals != themes[right].ReviewSignals {
			return themes[left].ReviewSignals > themes[right].ReviewSignals
		}
		if themes[left].LastIndex != themes[right].LastIndex {
			return themes[left].LastIndex > themes[right].LastIndex
		}

		return themes[left].Label < themes[right].Label
	})

	if len(themes) > weeknoteThemeLimit {
		themes = themes[:weeknoteThemeLimit]
	}

	return themes
}

func renderWeeknoteTheme(theme weeknoteTheme) string {
	latest := compactWeeknoteTextLimit(theme.Latest, 120)

	switch {
	case theme.BlockerSignals > 0 && theme.ReviewSignals > 0:
		return fmt.Sprintf(
			"`%s` kept pulling focus with %d %s and a lot of review churn. Latest turn: %s",
			theme.Label,
			theme.Mentions,
			pluralizeWeeknote("mention", theme.Mentions),
			latest,
		)
	case theme.BlockerSignals > 0:
		return fmt.Sprintf(
			"`%s` kept resurfacing as friction with %d %s. Latest turn: %s",
			theme.Label,
			theme.Mentions,
			pluralizeWeeknote("mention", theme.Mentions),
			latest,
		)
	default:
		return fmt.Sprintf(
			"`%s` was the main thread, showing up in %d %s. Latest turn: %s",
			theme.Label,
			theme.Mentions,
			pluralizeWeeknote("mention", theme.Mentions),
			latest,
		)
	}
}

func weeknoteEpisodeText(episode memory.Episode) string {
	summary := strings.TrimSpace(episode.Summary)
	if summary == "" {
		summary = strings.TrimSpace(episode.Text)
	}

	return compactWeeknoteText(summary)
}

func extractWeeknoteRefs(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	refs := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)

	for _, match := range weeknoteIssueKeyPattern.FindAllString(text, -1) {
		ref := strings.ToUpper(strings.TrimSpace(match))
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}

	for _, match := range weeknotePRPattern.FindAllString(text, -1) {
		ref := strings.ToUpper(strings.Join(strings.Fields(match), " "))
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}

	return refs
}

func weeknoteHasBlockerSignal(text string) bool {
	return strings.Contains(text, "block") ||
		strings.Contains(text, "stuck") ||
		strings.Contains(text, "unresolved") ||
		strings.Contains(text, "reassign") ||
		strings.Contains(text, "human review")
}

func weeknoteHasReviewSignal(text string) bool {
	return strings.Contains(text, "review") ||
		strings.Contains(text, "feedback") ||
		strings.Contains(text, "pr #")
}

func weeknoteHighlightKey(summary string, refs []string) string {
	if len(refs) > 0 {
		return "ref:" + refs[0]
	}

	return "text:" + strings.ToLower(summary)
}

func weeknoteRefsOverlap(refs []string, consumedRefs map[string]struct{}) bool {
	if len(refs) == 0 || len(consumedRefs) == 0 {
		return false
	}

	for _, ref := range refs {
		if _, ok := consumedRefs[ref]; ok {
			return true
		}
	}

	return false
}

func compactWeeknoteText(input string) string {
	return compactWeeknoteTextLimit(input, 180)
}

func compactWeeknoteTextLimit(input string, limit int) string {
	cleaned := strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
	if cleaned == "" {
		return ""
	}
	if limit <= 0 || len(cleaned) <= limit {
		return cleaned
	}

	return strings.TrimSpace(cleaned[:limit-1]) + "…"
}

func pluralizeWeeknote(noun string, count int) string {
	if count == 1 {
		return noun
	}

	return noun + "s"
}

func formatReflectionCues(episodes []memory.Episode) string {
	if len(episodes) == 0 {
		return "- Quiet window."
	}

	lines := make([]string, 0, 1+weeknoteThemeLimit+weeknoteHighlightLimit)
	sourceCounts := make(map[string]int, len(episodes))
	for _, episode := range episodes {
		source := strings.TrimSpace(episode.Source)
		if source == "" {
			source = "unknown"
		}
		sourceCounts[source]++
	}

	sources := make([]string, 0, len(sourceCounts))
	for source, count := range sourceCounts {
		sources = append(sources, fmt.Sprintf("%s=%d", source, count))
	}
	sort.Strings(sources)
	lines = append(lines, "- Source mix: "+strings.Join(sources, ", "))
	lines = append(lines, reflectionThemeLines(weeknoteThemes(episodes))...)

	for _, highlight := range weeknoteHighlights(episodes, nil) {
		lines = append(lines, "- Concrete moment: "+highlight)
	}

	return strings.Join(lines, "\n")
}

func reflectionThemeLines(themes []weeknoteTheme) []string {
	if len(themes) == 0 {
		return []string{"- Repeating thread: none obvious."}
	}

	lines := make([]string, 0, len(themes))
	for _, theme := range themes {
		lines = append(lines, "- Repeating thread: "+renderWeeknoteTheme(theme))
	}

	return lines
}
