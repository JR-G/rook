package memory

import "testing"

func TestExtractorIdentityProjectDecisionAndDeduping(t *testing.T) {
	t.Parallel()

	extractor := Extractor{}
	candidates := extractor.Candidates(Interaction{
		UserText: "My name is James. I am working on rook. We decided to ship rook. Be direct. Be direct.",
	})
	if len(candidates) < 4 {
		t.Fatalf("expected identity, project, decision, and style candidates, got %#v", candidates)
	}

	foundName := false
	foundProject := false
	foundDecision := false
	for _, candidate := range candidates {
		switch candidate.Type {
		case Fact:
			foundName = foundName || candidate.Subject == "name"
		case Preference, Person, Commitment, RelationshipNote, CommunicationStyleNote, OperatingPattern:
			continue
		case Project:
			foundProject = true
		case Decision:
			foundDecision = true
		default:
			t.Fatalf("unexpected candidate type %q", candidate.Type)
		}
	}
	if !foundName || !foundProject || !foundDecision {
		t.Fatalf("missing expected extracted candidates %#v", candidates)
	}
}

func TestExtractorEmptyAndRememberBranches(t *testing.T) {
	t.Parallel()

	extractor := Extractor{}
	if candidates := extractor.Candidates(Interaction{}); candidates != nil {
		t.Fatalf("expected empty interaction to return nil, got %#v", candidates)
	}
	if candidate := extractRememberCandidate("hello"); candidate != nil {
		t.Fatalf("expected non-remember text to be ignored, got %#v", candidate)
	}
	if candidate := extractRememberCandidate("remember "); candidate != nil {
		t.Fatalf("expected empty remember text to be ignored, got %#v", candidate)
	}
	if candidate := extractRememberCandidate("remember the codename is rook"); candidate == nil || candidate.Subject == "" {
		t.Fatalf("expected remember candidate, got %#v", candidate)
	}

	duplicates := dedupeCandidates([]Candidate{
		{Type: Fact, Scope: ScopeUser, Subject: "name"},
		{Type: Fact, Scope: ScopeUser, Subject: "name"},
		{Type: Fact, Scope: ScopeAgent, Subject: "name"},
	})
	if len(duplicates) != 2 {
		t.Fatalf("expected deduped candidates, got %#v", duplicates)
	}
}
