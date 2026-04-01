package memory

import (
	"math"
	"testing"
	"time"
)

func TestTokenize(t *testing.T) {
	t.Parallel()

	tokens := tokenize("Hello, rook 123")
	if len(tokens) != 3 {
		t.Fatalf("unexpected token count %d", len(tokens))
	}
}

func TestCosineSimilarity(t *testing.T) {
	t.Parallel()

	got := cosineSimilarity([]float64{1, 0}, []float64{1, 0})
	if math.Abs(got-1) > 0.0001 {
		t.Fatalf("unexpected cosine similarity %f", got)
	}
}

func TestKeywordScore(t *testing.T) {
	t.Parallel()

	got := keywordScore([]string{"alpha", "beta"}, []string{"beta"})
	if got != 0.5 {
		t.Fatalf("unexpected keyword score %f", got)
	}
}

func TestRecencyScore(t *testing.T) {
	t.Parallel()

	now := time.Now()
	if recencyScore(now, now) != 1 {
		t.Fatal("expected zero-age recency score to be 1")
	}
}
