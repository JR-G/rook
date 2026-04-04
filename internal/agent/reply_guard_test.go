package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/JR-G/rook/internal/memory"
)

func TestReplyGuardFirstMatchingAssistantText(t *testing.T) {
	t.Parallel()

	t.Run("empty episodes returns empty", func(t *testing.T) {
		t.Parallel()
		if got := firstMatchingAssistantText("hello world", nil); got != "" {
			t.Fatalf("expected empty string, got %q", got)
		}
	})

	t.Run("no assistant episodes returns empty", func(t *testing.T) {
		t.Parallel()
		episodes := []memory.Episode{
			{Source: "user", Text: "hey there"},
			{Source: "user", Text: "another message"},
		}
		if got := firstMatchingAssistantText("hello world", episodes); got != "" {
			t.Fatalf("expected empty string, got %q", got)
		}
	})

	t.Run("exact fingerprint match returns text", func(t *testing.T) {
		t.Parallel()
		episodes := []memory.Episode{
			{Source: "user", Text: "question"},
			{Source: "assistant", Text: "The quick brown fox jumps over the lazy dog."},
		}
		got := firstMatchingAssistantText("the quick brown fox jumps over the lazy dog", episodes)
		if got != "The quick brown fox jumps over the lazy dog." {
			t.Fatalf("expected matched text, got %q", got)
		}
	})

	t.Run("70 percent token overlap returns text", func(t *testing.T) {
		t.Parallel()
		// 10 tokens in the assistant text, reply shares 8 of them -> 80% overlap.
		assistantText := "alpha bravo charlie delta echo foxtrot golf hotel india juliet"
		replyText := "alpha bravo charlie delta echo foxtrot golf hotel kilo lima"
		episodes := []memory.Episode{
			{Source: "assistant", Text: assistantText},
		}
		got := firstMatchingAssistantText(replyText, episodes)
		if got != assistantText {
			t.Fatalf("expected overlap match, got %q", got)
		}
	})

	t.Run("below 70 percent overlap returns empty", func(t *testing.T) {
		t.Parallel()
		// 10 tokens each, only 4 shared -> 40% overlap.
		assistantText := "alpha bravo charlie delta echo foxtrot golf hotel india juliet"
		replyText := "alpha bravo charlie delta mike november oscar papa quebec romeo"
		episodes := []memory.Episode{
			{Source: "assistant", Text: assistantText},
		}
		got := firstMatchingAssistantText(replyText, episodes)
		if got != "" {
			t.Fatalf("expected empty for low overlap, got %q", got)
		}
	})

	t.Run("short replies skip overlap check", func(t *testing.T) {
		t.Parallel()
		// 5 tokens -- under the 6-token threshold so overlap is not checked.
		assistantText := "one two three four five"
		replyText := "one two three four five"
		episodes := []memory.Episode{
			{Source: "assistant", Text: assistantText},
		}
		// Exact fingerprint match still works even for short replies.
		got := firstMatchingAssistantText(replyText, episodes)
		if got != assistantText {
			t.Fatalf("expected exact match for short reply, got %q", got)
		}

		// Near-overlap but different fingerprint on short text should not match.
		nearReply := "one two three four six"
		got = firstMatchingAssistantText(nearReply, episodes)
		if got != "" {
			t.Fatalf("expected empty for short near-overlap, got %q", got)
		}
	})

	t.Run("checks all assistant turns not just last", func(t *testing.T) {
		t.Parallel()
		episodes := []memory.Episode{
			{Source: "assistant", Text: "first assistant reply with enough tokens to exceed the minimum"},
			{Source: "user", Text: "user follow-up"},
			{Source: "assistant", Text: "second completely different assistant reply here now"},
		}
		got := firstMatchingAssistantText("first assistant reply with enough tokens to exceed the minimum", episodes)
		if got != "first assistant reply with enough tokens to exceed the minimum" {
			t.Fatalf("expected to match earlier assistant turn, got %q", got)
		}
	})

	t.Run("empty reply returns empty", func(t *testing.T) {
		t.Parallel()
		episodes := []memory.Episode{
			{Source: "assistant", Text: "hello"},
		}
		if got := firstMatchingAssistantText("", episodes); got != "" {
			t.Fatalf("expected empty for empty reply, got %q", got)
		}
	})

	t.Run("falls back to summary when text is empty", func(t *testing.T) {
		t.Parallel()
		episodes := []memory.Episode{
			{Source: "assistant", Text: "", Summary: "the quick brown fox jumps over the lazy dog"},
		}
		got := firstMatchingAssistantText("the quick brown fox jumps over the lazy dog", episodes)
		if got != "the quick brown fox jumps over the lazy dog" {
			t.Fatalf("expected summary match, got %q", got)
		}
	})
}

func TestReplyGuardReplyFingerprint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty input", input: "", want: ""},
		{name: "preserves letters and numbers", input: "hello123world", want: "hello123world"},
		{name: "lowercases everything", input: "Hello WORLD", want: "hello world"},
		{name: "replaces punctuation with spaces", input: "hello,world!foo", want: "hello world foo"},
		{name: "collapses consecutive spaces", input: "hello   world", want: "hello world"},
		{name: "collapses mixed whitespace and punctuation", input: "hello... world!!!", want: "hello world"},
		{name: "trims leading and trailing whitespace", input: "  hello  ", want: "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := replyFingerprint(tt.input)
			if got != tt.want {
				t.Fatalf("replyFingerprint(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReplyGuardTokenOverlap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		left  []string
		right []string
		want  float64
	}{
		{name: "both empty", left: nil, right: nil, want: 0},
		{name: "left empty", left: nil, right: []string{"a"}, want: 0},
		{name: "right empty", left: []string{"a"}, right: nil, want: 0},
		{name: "identical slices", left: []string{"a", "b", "c"}, right: []string{"a", "b", "c"}, want: 1.0},
		{name: "no overlap", left: []string{"a", "b"}, right: []string{"c", "d"}, want: 0},
		{name: "partial overlap", left: []string{"a", "b", "c", "d"}, right: []string{"a", "b", "e", "f"}, want: 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tokenOverlap(tt.left, tt.right)
			if got != tt.want {
				t.Fatalf("tokenOverlap(%v, %v) = %f, want %f", tt.left, tt.right, got, tt.want)
			}
		})
	}
}

func TestReplyGuardRepeatedReplyRepairPrompt(t *testing.T) {
	t.Parallel()

	got := repeatedReplyRepairPrompt("some earlier text")
	if got == "" {
		t.Fatal("expected non-empty repair prompt")
	}
	if !strings.Contains(got, "some earlier text") {
		t.Fatalf("expected repair prompt to contain the earlier text, got %q", got)
	}
}

func TestReplyGuardFallbackRepeatedThreadReply(t *testing.T) {
	t.Parallel()

	t.Run("non-empty input", func(t *testing.T) {
		t.Parallel()
		got := fallbackRepeatedThreadReply("some text")
		if got == "" {
			t.Fatal("expected non-empty fallback")
		}
	})

	t.Run("empty input", func(t *testing.T) {
		t.Parallel()
		got := fallbackRepeatedThreadReply("")
		if got == "" {
			t.Fatal("expected non-empty fallback for empty input")
		}
	})

	t.Run("different messages for empty vs non-empty", func(t *testing.T) {
		t.Parallel()
		empty := fallbackRepeatedThreadReply("")
		nonEmpty := fallbackRepeatedThreadReply("something")
		if empty == nonEmpty {
			t.Fatalf("expected different fallback messages, both were %q", empty)
		}
	})
}

func TestReplyGuardRepairRepeatedThreadReply(t *testing.T) {
	t.Parallel()

	threadEpisodes := []memory.Episode{
		{Source: roleUser, Text: "how are you"},
		{Source: roleAssistant, Text: "I am watching for drift and trying to keep the week legible and clear"},
	}

	t.Run("no match passes through", func(t *testing.T) {
		t.Parallel()
		service, _ := newAgentTestService(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("chat should not be called for non-repeating reply")
			return nil, nil
		}))
		got, err := service.repairRepeatedThreadReply(
			context.Background(), service.snapshot(),
			"system", "user", threadEpisodes,
			"This is a completely different reply with unique content and wording",
		)
		if err != nil {
			t.Fatalf("repairRepeatedThreadReply: %v", err)
		}
		if got != "This is a completely different reply with unique content and wording" {
			t.Fatalf("expected passthrough, got %q", got)
		}
	})

	t.Run("exact match triggers repair", func(t *testing.T) {
		t.Parallel()
		service, _ := newAgentTestService(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == testAgentEmbedEndpoint {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[1,0]]}`)),
					Header:     make(http.Header),
				}, nil
			}
			body, _ := json.Marshal(map[string]any{
				"model":   "qwen3:4b",
				"message": map[string]any{"content": `{"answer":"Here is a fresh take on the situation."}`},
			})
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(string(body))),
				Header:     make(http.Header),
			}, nil
		}))
		got, err := service.repairRepeatedThreadReply(
			context.Background(), service.snapshot(),
			"system", "user", threadEpisodes,
			"I am watching for drift and trying to keep the week legible and clear",
		)
		if err != nil {
			t.Fatalf("repairRepeatedThreadReply: %v", err)
		}
		if got != "Here is a fresh take on the situation." {
			t.Fatalf("expected repaired reply, got %q", got)
		}
	})

	t.Run("chat failure returns fallback", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		service, _ := newAgentTestService(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == testAgentEmbedEndpoint {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[1,0]]}`)),
					Header:     make(http.Header),
				}, nil
			}
			callCount++
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader("server error")),
				Header:     make(http.Header),
			}, nil
		}))
		got, err := service.repairRepeatedThreadReply(
			context.Background(), service.snapshot(),
			"system", "user", threadEpisodes,
			"I am watching for drift and trying to keep the week legible and clear",
		)
		if err != nil {
			t.Fatalf("expected fallback not error: %v", err)
		}
		if !strings.Contains(got, "repeated myself") {
			t.Fatalf("expected fallback text, got %q", got)
		}
	})

	t.Run("repaired reply still repeating returns fallback", func(t *testing.T) {
		t.Parallel()
		service, _ := newAgentTestService(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == testAgentEmbedEndpoint {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[1,0]]}`)),
					Header:     make(http.Header),
				}, nil
			}
			body, _ := json.Marshal(map[string]any{
				"model":   "qwen3:4b",
				"message": map[string]any{"content": `{"answer":"I am watching for drift and trying to keep the week legible and clear"}`},
			})
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(string(body))),
				Header:     make(http.Header),
			}, nil
		}))
		got, err := service.repairRepeatedThreadReply(
			context.Background(), service.snapshot(),
			"system", "user", threadEpisodes,
			"I am watching for drift and trying to keep the week legible and clear",
		)
		if err != nil {
			t.Fatalf("expected fallback not error: %v", err)
		}
		if !strings.Contains(got, "repeated myself") {
			t.Fatalf("expected fallback for still-repeating repair, got %q", got)
		}
	})
}
