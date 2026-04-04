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
		{input: `{"unused":"reply"}`, want: ""},
		{input: "not json", want: ""},
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

func TestCleanBlocksUnstructuredLeak(t *testing.T) {
	t.Parallel()

	filter := New()
	got := filter.Clean(`We are given a user request: "hi"
Relevant memory:
Working context:
Historical episodes:`)
	if got != "I generated internal output instead of a user-facing reply. Please try again." {
		t.Fatalf("unexpected unstructured fallback %q", got)
	}
}

func TestExtractStructuredReply(t *testing.T) {
	t.Parallel()

	reply, ok := extractStructuredReply("<final>Hello there.</final>")
	if !ok || reply != "Hello there." {
		t.Fatalf("unexpected final reply %#v ok=%t", reply, ok)
	}

	reply, ok = extractStructuredReply(`{"answer":"Hello again."}`)
	if !ok || reply != "Hello again." {
		t.Fatalf("unexpected json reply %#v ok=%t", reply, ok)
	}

	if _, ok = extractStructuredReply("plain text"); ok {
		t.Fatal("did not expect plain text to be accepted as structured output")
	}
}
