package memory

import (
	"regexp"
	"strings"
)

var (
	preferencePattern = regexp.MustCompile(`(?i)\b(i prefer|i like|i dislike|i don't like|i hate)\b\s+([^.!?]+)`)
	namePattern       = regexp.MustCompile(`(?i)\b(my name is|call me)\b\s+([a-z0-9 .'-]+)`)
	projectPattern    = regexp.MustCompile(`(?i)\b(i am working on|i'm working on|i work on)\b\s+([^.!?]+)`)
	decisionPattern   = regexp.MustCompile(`(?i)\b(we decided to|decision:)\b\s*([^.!?]+)`)
)

// Interaction captures the text used for durable memory extraction.
type Interaction struct {
	UserText      string
	AssistantText string
}

// Extractor applies explicit rules for durable memory writes.
type Extractor struct{}

// Candidates returns durable memory candidates worth storing.
func (Extractor) Candidates(interaction Interaction) []Candidate {
	text := strings.TrimSpace(interaction.UserText)
	if text == "" {
		return nil
	}

	candidates := make([]Candidate, 0, 6)
	candidates = append(candidates, extractPreferenceCandidates(text)...)
	candidates = append(candidates, extractIdentityCandidates(text)...)
	candidates = append(candidates, extractProjectCandidates(text)...)
	candidates = append(candidates, extractDecisionCandidates(text)...)
	candidates = append(candidates, extractStyleCandidates(text)...)

	if remember := extractRememberCandidate(text); remember != nil {
		candidates = append(candidates, *remember)
	}

	return dedupeCandidates(candidates)
}

func extractPreferenceCandidates(text string) []Candidate {
	matches := preferencePattern.FindAllStringSubmatch(text, -1)
	candidates := make([]Candidate, 0, len(matches))
	for _, match := range matches {
		body := strings.TrimSpace(match[2])
		if body == "" {
			continue
		}

		candidates = append(candidates, Candidate{
			Type:       Preference,
			Scope:      ScopeUser,
			Subject:    normaliseSubject(body),
			Body:       body,
			Keywords:   tokenize(body),
			Confidence: 0.92,
			Importance: 0.74,
			Source:     "user",
		})
	}

	return candidates
}

func extractIdentityCandidates(text string) []Candidate {
	match := namePattern.FindStringSubmatch(text)
	if len(match) == 0 {
		return nil
	}

	name := strings.TrimSpace(match[2])
	if name == "" {
		return nil
	}

	candidateType := Fact
	subject := "preferred_name"
	if strings.EqualFold(strings.TrimSpace(match[1]), "my name is") {
		subject = "name"
	}

	return []Candidate{{
		Type:       candidateType,
		Scope:      ScopeUser,
		Subject:    subject,
		Body:       name,
		Keywords:   tokenize(name),
		Confidence: 0.98,
		Importance: 0.95,
		Source:     "user",
	}}
}

func extractProjectCandidates(text string) []Candidate {
	match := projectPattern.FindStringSubmatch(text)
	if len(match) == 0 {
		return nil
	}

	project := strings.TrimSpace(match[2])
	if project == "" {
		return nil
	}

	return []Candidate{{
		Type:       Project,
		Scope:      ScopeUser,
		Subject:    normaliseSubject(project),
		Body:       project,
		Keywords:   tokenize(project),
		Confidence: 0.82,
		Importance: 0.7,
		Source:     "user",
	}}
}

func extractDecisionCandidates(text string) []Candidate {
	match := decisionPattern.FindStringSubmatch(text)
	if len(match) == 0 {
		return nil
	}

	decision := strings.TrimSpace(match[2])
	if decision == "" {
		return nil
	}

	return []Candidate{{
		Type:       Decision,
		Scope:      ScopeUser,
		Subject:    normaliseSubject(decision),
		Body:       decision,
		Keywords:   tokenize(decision),
		Confidence: 0.84,
		Importance: 0.78,
		Source:     "user",
	}}
}

func extractStyleCandidates(text string) []Candidate {
	lowerText := strings.ToLower(text)
	stylePhrases := []struct {
		Needle string
		Body   string
	}{
		{Needle: "be concise", Body: "Prefer concise replies."},
		{Needle: "be direct", Body: "Prefer direct replies."},
		{Needle: "use bullets", Body: "Use bullets when they clarify."},
		{Needle: "no emojis", Body: "Avoid emojis."},
		{Needle: "don't be chatty", Body: "Avoid chatty or padded replies."},
	}

	candidates := make([]Candidate, 0, len(stylePhrases))
	for _, phrase := range stylePhrases {
		if !strings.Contains(lowerText, phrase.Needle) {
			continue
		}

		candidates = append(candidates, Candidate{
			Type:       CommunicationStyleNote,
			Scope:      ScopeUser,
			Subject:    normaliseSubject(phrase.Body),
			Body:       phrase.Body,
			Keywords:   tokenize(phrase.Body),
			Confidence: 0.95,
			Importance: 0.82,
			Source:     "user",
		})
	}

	return candidates
}

func extractRememberCandidate(text string) *Candidate {
	lowerText := strings.ToLower(text)
	if !strings.HasPrefix(lowerText, "remember ") && !strings.HasPrefix(lowerText, "remember that ") {
		return nil
	}

	body := strings.TrimSpace(strings.TrimPrefix(text, "remember "))
	body = strings.TrimSpace(strings.TrimPrefix(body, "that "))
	if body == "" {
		return nil
	}

	return &Candidate{
		Type:       Fact,
		Scope:      ScopeUser,
		Subject:    normaliseSubject(body),
		Body:       body,
		Keywords:   tokenize(body),
		Confidence: 0.88,
		Importance: 0.9,
		Source:     "user",
	}
}

func dedupeCandidates(candidates []Candidate) []Candidate {
	seen := make(map[string]struct{}, len(candidates))
	deduped := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		key := strings.Join([]string{string(candidate.Type), candidate.Scope, candidate.Subject}, "::")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, candidate)
	}

	return deduped
}
