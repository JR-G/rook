package agent

import (
	"context"
	"encoding/json"
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

type stubSearcher struct {
	results []web.Result
}

func (s stubSearcher) Enabled() bool    { return true }
func (s stubSearcher) Provider() string { return "stub" }
func (s stubSearcher) Search(context.Context, string, int) ([]web.Result, error) {
	return s.results, nil
}

func TestRespondWritesEpisodesAndUsesWeb(t *testing.T) {
	t.Parallel()

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
		ollama.NewWithHTTPClient("http://ollama.test", time.Second, time.Second, &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case testAgentChatEndpoint:
					body, _ := json.Marshal(map[string]any{
						"model": "phi4-mini",
						"message": map[string]any{
							"content": "<final>Here is the answer.</final>",
						},
					})
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(string(body))),
						Header:     make(http.Header),
					}, nil
				case testAgentEmbedEndpoint:
					body, _ := json.Marshal(map[string]any{
						"embeddings": [][]float64{{1, 0}},
					})
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(string(body))),
						Header:     make(http.Header),
					}, nil
				default:
					t.Fatalf("unexpected path %s", r.URL.Path)
					return nil, nil
				}
			}),
		}),
		store,
		personaManager,
		stubSearcher{results: []web.Result{{Title: "A", URL: "https://example.com", Snippet: "B"}}},
		output.New(),
		Config{
			ChatModel:          "phi4-mini",
			EmbeddingModel:     "nomic-embed-text",
			Temperature:        0.2,
			MinWriteImportance: 0.6,
			MaxPromptItems:     4,
			MaxEpisodeItems:    2,
			WebEnabled:         true,
			WebMaxResults:      2,
			AutoOnFreshness:    true,
		},
	)

	response, err := service.Respond(context.Background(), Request{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Text:      "what is the latest release?",
	})
	if err != nil {
		t.Fatalf("respond failed: %v", err)
	}
	if !response.UsedWeb {
		t.Fatal("expected live web lookup to be used")
	}
	if !strings.Contains(response.Text, "Live web lookup used.") {
		t.Fatalf("expected web notice, got %q", response.Text)
	}

	items, err := store.ListRecentMemories(context.Background(), 10)
	if err != nil {
		t.Fatalf("list recent memories failed: %v", err)
	}
	_ = items

	episodes, err := store.RecentEpisodes(context.Background(), 10)
	if err != nil {
		t.Fatalf("recent episodes failed: %v", err)
	}
	if len(episodes) < 2 {
		t.Fatalf("expected user and assistant episodes, got %d", len(episodes))
	}
}

func TestAgentConfigHelpers(t *testing.T) {
	t.Parallel()

	service := &Service{
		config: Config{
			ChatModel:       "phi4-mini",
			EmbeddingModel:  "nomic-embed-text",
			WebEnabled:      true,
			AutoOnFreshness: true,
		},
		searcher: web.NoopSearcher{},
	}

	service.SetChatModel("updated-model")
	if service.ChatModel() != "updated-model" {
		t.Fatalf("unexpected chat model %q", service.ChatModel())
	}
	if service.EmbeddingModel() != "nomic-embed-text" {
		t.Fatalf("unexpected embedding model %q", service.EmbeddingModel())
	}

	service.UpdateConfig(Config{ChatModel: "next", EmbeddingModel: "embed"}, stubSearcher{})
	if service.ChatModel() != "next" {
		t.Fatalf("unexpected updated model %q", service.ChatModel())
	}
	if !service.shouldUseWeb(Config{WebEnabled: true, AutoOnFreshness: true}, "latest release") {
		t.Fatal("expected freshness query to trigger web")
	}
	if renderItems(nil) != "- none" || renderEpisodes(nil) != "- none" {
		t.Fatal("expected empty render helpers")
	}

	prompt := buildUserPrompt("hello", memory.RetrievalContext{}, nil, false)
	if !strings.Contains(prompt, "Internal context below is for reasoning only.") {
		t.Fatalf("expected internal-context prompt guard, got %q", prompt)
	}
	if !strings.Contains(prompt, "Reply now with exactly one <final> block.") {
		t.Fatalf("expected final-answer prompt guard, got %q", prompt)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
