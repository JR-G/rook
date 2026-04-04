package output

import (
	"strings"
	"testing"
)

func TestCleanRemovesInternalNoise(t *testing.T) {
	t.Parallel()

	filter := New()
	got := filter.Clean("<think>private</think>\n<final>hello</final>")
	if got != "hello" {
		t.Fatalf("unexpected cleaned output %q", got)
	}
}

func TestCleanExtractsJSONAnswer(t *testing.T) {
	t.Parallel()

	filter := New()
	got := filter.Clean(`{"answer":"plain reply"}`)
	if got != "plain reply" {
		t.Fatalf("unexpected json extraction %q", got)
	}
}

func TestCleanExtractsFinalReplyBlock(t *testing.T) {
	t.Parallel()

	filter := New()
	got := filter.Clean(`analysis that should not pass through
<final>Hi. What do you need?</final>`)
	if got != "Hi. What do you need?" {
		t.Fatalf("unexpected final-block extraction %q", got)
	}
}

func TestCleanTruncatesLongOutput(t *testing.T) {
	t.Parallel()

	filter := Filter{MaxChars: 10}
	got := filter.Clean("<final>" + strings.Repeat("a", 20) + "</final>")
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected truncation suffix, got %q", got)
	}
}

func TestCleanEmptyFallback(t *testing.T) {
	t.Parallel()

	filter := New()
	if got := filter.Clean("   "); !strings.Contains(got, "clean reply") {
		t.Fatalf("unexpected empty fallback %q", got)
	}
}
