package output

import "testing"

func TestExtractPrimaryTextVariants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		want  string
	}{
		{input: `{"response":"reply"}`, want: "reply"},
		{input: `{"content":"reply"}`, want: "reply"},
		{input: `{"text":"reply"}`, want: "reply"},
		{input: `{"message":"reply"}`, want: "reply"},
		{input: `{"unused":"reply"}`, want: `{"unused":"reply"}`},
		{input: "not json", want: "not json"},
	}

	for _, testCase := range cases {
		if got := extractPrimaryText(testCase.input); got != testCase.want {
			t.Fatalf("unexpected primary text for %q: %q", testCase.input, got)
		}
	}
}

func TestRemoveInternalLinesAndInternalOnlyFallback(t *testing.T) {
	t.Parallel()

	cleaned := removeInternalLines("Tool: search\nProvider payload: raw\nVisible line")
	if cleaned != "Visible line" {
		t.Fatalf("unexpected cleaned lines %q", cleaned)
	}
	cleaned = removeInternalLines("Analysis: hidden\n\nVisible line\nInternal note: hidden")
	if cleaned != "\nVisible line" {
		t.Fatalf("unexpected cleaned lines with blanks %q", cleaned)
	}

	filter := New()
	if got := filter.Clean("Tool: search\nInternal note: hidden"); got == "" || got == "Visible line" || got == "Tool: search" {
		t.Fatalf("unexpected internal-only fallback %q", got)
	}
}
