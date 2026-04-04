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

func TestCleanUnwrapsNestedBlockWrappers(t *testing.T) {
	t.Parallel()

	filter := New()
	got := filter.Clean(`{"text":"block:\n<final>\nRook keeps an eye on the weather of the work.\n"}`)
	if got != "Rook keeps an eye on the weather of the work." {
		t.Fatalf("unexpected nested block cleanup %q", got)
	}
}

func TestCleanUnwrapsPlainTextBlockWrapper(t *testing.T) {
	t.Parallel()

	filter := New()
	got := filter.Clean("block with the reply inside.\nImportant: Do not add any extra text outside the block.\nResponse:\n<final>\nI'm functioning optimally and ready to support you.")
	if got != "I'm functioning optimally and ready to support you." {
		t.Fatalf("unexpected plain-text block cleanup %q", got)
	}
}

func TestCleanStripsScaffoldingLines(t *testing.T) {
	t.Parallel()

	filter := New()
	got := filter.Clean("<final>\nLet me write:\nI will write:\nResponse:\n<final>\nKeep the thread steady.\n</final>")
	if got != "Keep the thread steady." {
		t.Fatalf("unexpected scaffolding cleanup %q", got)
	}
}

func TestUnwrapStructuredTextHelpers(t *testing.T) {
	t.Parallel()

	reply, ok := unwrapStructuredText("reply: steady course")
	if !ok || reply != "steady course" {
		t.Fatalf("unexpected reply unwrap %#v ok=%t", reply, ok)
	}

	reply, ok = extractOpenFinalRemainder("block:\n<final>\nKeep moving.")
	if !ok || reply != "Keep moving." {
		t.Fatalf("unexpected open final unwrap %#v ok=%t", reply, ok)
	}

	if _, ok = extractOpenFinalRemainder("plain text"); ok {
		t.Fatal("did not expect plain text to look like an open final block")
	}
	if _, ok = unwrapStructuredText("   "); ok {
		t.Fatal("did not expect blank text to unwrap")
	}
}

func TestStructuredExtractionEdgeCases(t *testing.T) {
	t.Parallel()

	if got := extractPrimaryText(`["reply"]`); got != `["reply"]` {
		t.Fatalf("unexpected non-object json fallback %q", got)
	}

	reply, ok := unwrapStructuredText("final: <final>Trim to the point.</final>")
	if !ok || reply != "Trim to the point." {
		t.Fatalf("unexpected final-prefix unwrap %#v ok=%t", reply, ok)
	}

	reply, ok = extractOpenFinalRemainder("<final>\nKeep the thread alive.\n</final>")
	if !ok || reply != "Keep the thread alive." {
		t.Fatalf("unexpected closed final remainder %#v ok=%t", reply, ok)
	}
}

func TestLooksLikeStructuredWrapper(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		input string
		want  bool
	}{
		{input: "", want: false},
		{input: `{"text":"<final>json should route through json extraction"}`, want: false},
		{input: "response: <final>hello", want: true},
		{input: "plain text\n<final>\nhello", want: true},
		{input: "just plain text", want: false},
	}

	for _, testCase := range testCases {
		if got := looksLikeStructuredWrapper(testCase.input); got != testCase.want {
			t.Fatalf("looksLikeStructuredWrapper(%q) = %t, want %t", testCase.input, got, testCase.want)
		}
	}
}
