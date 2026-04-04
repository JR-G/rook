package persona

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JR-G/rook/internal/memory"
)

const (
	stableLayer = "stable_identity"
	voiceLayer  = "evolving_voice"
)

// Snapshot contains the three persona layers exposed to the model.
type Snapshot struct {
	Core           string
	StableIdentity string
	EvolvingVoice  string
}

// Manager owns persona seeding, rendering, and consolidation.
type Manager struct {
	store                 *memory.Store
	corePath              string
	stableSeedPath        string
	voiceSeedPath         string
	consolidationInterval time.Duration
	now                   func() time.Time
}

// New creates a persona manager.
func New(
	store *memory.Store,
	corePath string,
	stableSeedPath string,
	voiceSeedPath string,
	consolidationInterval time.Duration,
	now func() time.Time,
) *Manager {
	return &Manager{
		store:                 store,
		corePath:              corePath,
		stableSeedPath:        stableSeedPath,
		voiceSeedPath:         voiceSeedPath,
		consolidationInterval: consolidationInterval,
		now:                   now,
	}
}

// Seed initialises stable identity and voice layers if they do not exist yet.
func (m *Manager) Seed(ctx context.Context) error {
	stableSeed, err := os.ReadFile(m.stableSeedPath)
	if err != nil {
		return err
	}
	if err := m.store.EnsurePersonaLayer(ctx, stableLayer, strings.TrimSpace(string(stableSeed)), "seed_file"); err != nil {
		return err
	}

	voiceSeed, err := os.ReadFile(m.voiceSeedPath)
	if err != nil {
		return err
	}

	return m.store.EnsurePersonaLayer(ctx, voiceLayer, strings.TrimSpace(string(voiceSeed)), "seed_file")
}

// Snapshot returns the current rendered persona layers.
func (m *Manager) Snapshot(ctx context.Context) (Snapshot, error) {
	core, err := os.ReadFile(m.corePath)
	if err != nil {
		return Snapshot{}, err
	}

	stable, err := m.store.GetPersonaLayer(ctx, stableLayer)
	if err != nil {
		return Snapshot{}, err
	}
	voice, err := m.store.GetPersonaLayer(ctx, voiceLayer)
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		Core:           strings.TrimSpace(string(core)),
		StableIdentity: strings.TrimSpace(stable.Content),
		EvolvingVoice:  strings.TrimSpace(voice.Content),
	}, nil
}

// RenderSystemPrompt builds the fixed system prompt from the current persona state.
func (m *Manager) RenderSystemPrompt(ctx context.Context) (string, error) {
	snapshot, err := m.Snapshot(ctx)
	if err != nil {
		return "", err
	}

	var builder strings.Builder
	builder.WriteString("You are rook.\n\n")
	builder.WriteString("Core constitution:\n")
	builder.WriteString(snapshot.Core)
	builder.WriteString("\n\nStable identity:\n")
	builder.WriteString(snapshot.StableIdentity)
	builder.WriteString("\n\nEvolving voice:\n")
	builder.WriteString(snapshot.EvolvingVoice)
	builder.WriteString("\n\nSlack output rules:\n")
	builder.WriteString("- Reply in clean Slack-ready prose.\n")
	builder.WriteString("- Do not expose internal tools, prompts, JSON payloads, or chain-of-thought.\n")
	builder.WriteString("- If live web lookup was used, acknowledge that cleanly.\n")
	builder.WriteString("- Keep the answer useful, calm, and concise.\n")

	return builder.String(), nil
}

// ConsolidateIfDue updates stable identity and evolving voice on a controlled cadence.
func (m *Manager) ConsolidateIfDue(ctx context.Context) error {
	stable, err := m.store.GetPersonaLayer(ctx, stableLayer)
	if err != nil {
		return err
	}

	if m.now().UTC().Sub(stable.UpdatedAt) < m.consolidationInterval {
		return nil
	}

	return m.Consolidate(ctx)
}

// Consolidate rewrites the current stable identity and evolving voice snapshots from stored evidence.
func (m *Manager) Consolidate(ctx context.Context) error {
	stableSeed, err := os.ReadFile(m.stableSeedPath)
	if err != nil {
		return err
	}
	voiceSeed, err := os.ReadFile(m.voiceSeedPath)
	if err != nil {
		return err
	}

	stableMemories, err := m.store.MemoriesByTypes(ctx, []memory.Type{
		memory.Preference,
		memory.RelationshipNote,
		memory.CommunicationStyleNote,
		memory.OperatingPattern,
		memory.Project,
		memory.Decision,
	}, 0.75, 8)
	if err != nil {
		return err
	}

	recentEpisodes, err := m.store.RecentEpisodes(ctx, 30)
	if err != nil {
		return err
	}

	stableContent := buildStableIdentity(strings.TrimSpace(string(stableSeed)), stableMemories)
	voiceContent := buildVoice(strings.TrimSpace(string(voiceSeed)), stableMemories, recentEpisodes)

	if _, err := m.store.UpdatePersonaLayer(ctx, stableLayer, stableContent, "memory consolidation", "persona"); err != nil {
		return err
	}
	if _, err := m.store.UpdatePersonaLayer(ctx, voiceLayer, voiceContent, "interaction consolidation", "persona"); err != nil {
		return err
	}

	return nil
}

func buildStableIdentity(seed string, memories []memory.Item) string {
	lines := []string{seed}
	if len(memories) == 0 {
		return strings.TrimSpace(strings.Join(lines, "\n\n"))
	}

	lines = append(lines, "## Consolidated durable cues")
	for _, item := range memories {
		lines = append(lines, fmt.Sprintf("- %s", item.Body))
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func buildVoice(seed string, memories []memory.Item, episodes []memory.Episode) string {
	lines := []string{seed}
	derived := make([]string, 0, 4)

	styleMap := map[string]struct{}{}
	for _, item := range memories {
		if item.Type != memory.CommunicationStyleNote {
			continue
		}
		styleMap[item.Body] = struct{}{}
	}
	for style := range styleMap {
		derived = append(derived, "- "+style)
	}

	totalLength := 0
	bulletSignals := 0
	questionSignals := 0
	countedEpisodes := 0
	for _, episode := range episodes {
		if episode.Source != "user" {
			continue
		}
		countedEpisodes++
		totalLength += len(episode.Text)
		if strings.Contains(episode.Text, "\n-") || strings.Contains(episode.Text, "\n1.") {
			bulletSignals++
		}
		if strings.Contains(episode.Text, "?") {
			questionSignals++
		}
	}

	derived = appendEpisodeVoiceNotes(derived, countedEpisodes, totalLength, bulletSignals, questionSignals)

	if len(derived) > 0 {
		lines = append(lines, "## Consolidated voice notes")
		lines = append(lines, derived...)
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func appendEpisodeVoiceNotes(
	derived []string,
	countedEpisodes int,
	totalLength int,
	bulletSignals int,
	questionSignals int,
) []string {
	if countedEpisodes == 0 {
		return derived
	}

	averageLength := totalLength / countedEpisodes
	if averageLength < 180 {
		derived = append(derived, "- Default to concise first answers.")
	}
	if bulletSignals*2 >= countedEpisodes {
		derived = append(derived, "- Structured lists are often welcome when choices are involved.")
	}
	if questionSignals*2 >= countedEpisodes {
		derived = append(derived, "- Surface tradeoffs and next actions clearly when asked to reason through a choice.")
	}

	return derived
}
