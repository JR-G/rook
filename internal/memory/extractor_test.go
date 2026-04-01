package memory

import "testing"

func TestExtractorFindsPreferenceAndStyle(t *testing.T) {
	t.Parallel()

	extractor := Extractor{}
	candidates := extractor.Candidates(Interaction{
		UserText: "I prefer short replies and be concise with no emojis.",
	})
	if len(candidates) < 2 {
		t.Fatalf("expected multiple candidates, got %d", len(candidates))
	}
}

func TestExtractorRemember(t *testing.T) {
	t.Parallel()

	extractor := Extractor{}
	candidates := extractor.Candidates(Interaction{
		UserText: "remember that the project codename is rook",
	})
	if len(candidates) != 1 {
		t.Fatalf("expected one explicit remember candidate, got %d", len(candidates))
	}
	if candidates[0].Type != Fact {
		t.Fatalf("unexpected type %q", candidates[0].Type)
	}
}
