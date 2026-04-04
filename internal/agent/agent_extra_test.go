package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JR-G/rook/internal/memory"
	"github.com/JR-G/rook/internal/ollama"
	"github.com/JR-G/rook/internal/output"
	"github.com/JR-G/rook/internal/persona"
	"github.com/JR-G/rook/internal/tools/web"
)

const (
	testAgentChatEndpoint  = "/api/chat"
	testAgentEmbedEndpoint = "/api/embed"
)

type errSearcher struct{}

func (errSearcher) Enabled() bool    { return true }
func (errSearcher) Provider() string { return "err" }
func (errSearcher) Search(context.Context, string, int) ([]web.Result, error) {
	return nil, errors.New("search failed")
}

func TestPersistCandidateAndHelperRendering(t *testing.T) {
	t.Parallel()

	service, store := newAgentTestService(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case testAgentEmbedEndpoint:
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[1,0,0]]}`)),
				Header:     make(http.Header),
			}, nil
		case testAgentChatEndpoint:
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"model":"qwen3:4b","message":{"content":"{\"answer\":\"ok\"}"}}`)),
				Header:     make(http.Header),
			}, nil
		default:
			return nil, errors.New("unexpected path")
		}
	}))

	if err := service.persistCandidate(context.Background(), service.snapshot(), memory.Candidate{
		Type:       memory.Preference,
		Scope:      memory.ScopeUser,
		Subject:    "tone",
		Body:       "Prefer concise replies.",
		Confidence: 0.9,
		Importance: 0.8,
	}); err != nil {
		t.Fatalf("persist candidate: %v", err)
	}

	if err := service.persistCandidate(context.Background(), service.snapshot(), memory.Candidate{
		Type:       memory.Preference,
		Scope:      memory.ScopeUser,
		Subject:    "voice",
		Body:       "Use direct language.",
		Embedding:  []float64{1, 1},
		Confidence: 0.9,
		Importance: 0.8,
	}); err != nil {
		t.Fatalf("persist candidate with embedding: %v", err)
	}

	items, err := store.ListRecentMemories(context.Background(), 10)
	if err != nil || len(items) < 2 {
		t.Fatalf("unexpected stored memories %#v err=%v", items, err)
	}

	rendered := renderItems([]memory.Item{{Type: memory.Preference, Body: "Prefer concise replies."}})
	if !strings.Contains(rendered, "Prefer concise replies.") {
		t.Fatalf("unexpected rendered items %q", rendered)
	}
	renderedEpisodes := renderEpisodes([]memory.Episode{{Source: "user", Summary: "Asked a question"}})
	if !strings.Contains(renderedEpisodes, "Asked a question") {
		t.Fatalf("unexpected rendered episodes %q", renderedEpisodes)
	}
	contextText := renderMemoryContext(memory.RetrievalContext{
		UserFacts:      []memory.Item{{Type: memory.Fact, Body: "Name is James"}},
		WorkingContext: []memory.Item{{Type: memory.Project, Body: "Working on rook"}},
		Squad0Episodes: []memory.Episode{{Source: "squad0", Summary: "update"}},
	})
	if !strings.Contains(contextText, "Recent squad0 context") {
		t.Fatalf("unexpected memory context %q", contextText)
	}
}

func TestChatFallbackAndWebHelpers(t *testing.T) {
	t.Parallel()

	service, _ := newAgentTestService(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case testAgentChatEndpoint:
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader("boom")),
				Header:     make(http.Header),
			}, nil
		case testAgentEmbedEndpoint:
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[1,0]]}`)),
				Header:     make(http.Header),
			}, nil
		default:
			return nil, errors.New("unexpected path")
		}
	}))

	if _, err := service.chatWithFallback(context.Background(), Config{}, nil, output.AnswerSchema()); err == nil {
		t.Fatal("expected empty chat config to fail")
	}
	if _, err := service.chatWithFallback(
		context.Background(),
		service.snapshot(),
		[]ollama.Message{{Role: "user", Content: "hi"}},
		output.AnswerSchema(),
	); err == nil {
		t.Fatal("expected non-fallback chat error")
	}

	if results, used := service.webContext(context.Background(), Config{WebEnabled: false}, "latest release"); used || len(results) != 0 {
		t.Fatalf("expected disabled web to bypass search, got %#v used=%t", results, used)
	}

	service.searcher = errSearcher{}
	if results, used := service.webContext(context.Background(), Config{WebEnabled: true, WebMaxResults: 3}, "latest release"); used || len(results) != 0 {
		t.Fatalf("expected failing searcher to be ignored, got %#v used=%t", results, used)
	}

	models := candidateModels("qwen3:4b", []string{"phi4-mini", "qwen3:4b", "phi4-mini"})
	if len(models) != 2 || models[1] != "phi4-mini" {
		t.Fatalf("unexpected candidate models %#v", models)
	}
	if !service.shouldUseWeb(Config{WebEnabled: true, AutoOnFreshness: true}, "check the latest release") {
		t.Fatal("expected explicit trigger to use web")
	}
	if service.shouldUseWeb(Config{WebEnabled: false}, "latest release") {
		t.Fatal("did not expect disabled web config to use web")
	}

	prompt := buildUserPrompt("what changed?", memory.RetrievalContext{}, nil, []web.Result{{Title: "A", URL: "https://example.com"}}, true)
	if !strings.Contains(prompt, "Live web results") {
		t.Fatalf("unexpected user prompt %q", prompt)
	}
}

func newAgentTestService(t *testing.T, transport http.RoundTripper) (*Service, *memory.Store) {
	t.Helper()

	tempDir := t.TempDir()
	store, err := memory.OpenWithClock(filepath.Join(tempDir, "rook.sqlite"), time.Now)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	corePath := filepath.Join(tempDir, "core.md")
	stablePath := filepath.Join(tempDir, "stable.md")
	voicePath := filepath.Join(tempDir, "voice.md")
	for path, content := range map[string]string{
		corePath:   "core",
		stablePath: "stable",
		voicePath:  "voice",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write persona file: %v", err)
		}
	}

	personaManager := persona.New(store, corePath, stablePath, voicePath, time.Hour, time.Now)
	if err := personaManager.Seed(context.Background()); err != nil {
		t.Fatalf("seed persona: %v", err)
	}

	service := New(
		ollama.NewWithHTTPClient("http://ollama.test", time.Second, time.Second, &http.Client{Transport: transport}),
		store,
		personaManager,
		web.NoopSearcher{},
		Config{
			ChatModel:          "qwen3:4b",
			ChatFallbacks:      []string{"phi4-mini"},
			EmbeddingModel:     "nomic-embed-text",
			Temperature:        0.7,
			MinWriteImportance: 0.6,
			MaxPromptItems:     4,
			MaxEpisodeItems:    2,
			WebEnabled:         true,
			WebMaxResults:      3,
			AutoOnFreshness:    true,
		},
	)

	return service, store
}
