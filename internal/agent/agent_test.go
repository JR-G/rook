package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
							"content": `{"answer":"Here is the answer."}`,
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
		nil,
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

	prompt := buildUserPrompt("hello", memory.RetrievalContext{}, nil, "", nil, false, nil)
	if !strings.Contains(prompt, "Internal context below is for reasoning only.") {
		t.Fatalf("expected internal-context prompt guard, got %q", prompt)
	}
	if !strings.Contains(prompt, output.AnswerSchemaString()) {
		t.Fatalf("expected answer schema in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Let rook's personality come through even in practical answers.") {
		t.Fatalf("expected general voice guidance in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Conversational guidance:") {
		t.Fatalf("expected conversational guidance in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "rook's own perspective") {
		t.Fatalf("expected rook-perspective guidance in prompt, got %q", prompt)
	}

	retrieval := memory.RetrievalContext{
		Episodes: []memory.Episode{
			{ChannelID: "C1", ThreadTS: "1.0", Source: "assistant", Summary: "Current thread reply"},
			{ChannelID: "C2", ThreadTS: "2.0", Source: "assistant", Summary: "Older historical reply"},
		},
	}
	ordinary := adjustRetrievalForQuery("C1", "1.0", nil, retrieval)
	if len(ordinary.Episodes) != 1 || ordinary.Episodes[0].ChannelID != "C2" {
		t.Fatalf("unexpected ordinary-query retrieval %#v", ordinary.Episodes)
	}
	threadAdjusted := adjustRetrievalForQuery("C1", "1.0", []memory.Episode{
		{Source: "assistant", Text: "Earlier reply."},
	}, retrieval)
	if len(threadAdjusted.Episodes) != 0 {
		t.Fatalf("expected active-thread retrieval to drop historical episodes, got %#v", threadAdjusted.Episodes)
	}

	threadPrompt := buildUserPrompt(
		"oh really?",
		memory.RetrievalContext{},
		[]memory.Episode{
			{Source: "user", Summary: "how are you today?"},
			{Source: "assistant", Summary: "Focused on keeping your week legible."},
		},
		"",
		nil,
		false,
		nil,
	)
	if !strings.Contains(threadPrompt, "Thread continuation instructions:") {
		t.Fatalf("expected thread continuation instructions in prompt, got %q", threadPrompt)
	}
	if !strings.Contains(threadPrompt, "Continue it naturally") {
		t.Fatalf("expected continuation guidance in prompt, got %q", threadPrompt)
	}
	if !strings.Contains(threadPrompt, "short follow-up") {
		t.Fatalf("expected short follow-up guidance in prompt, got %q", threadPrompt)
	}

	memoryPrompt := buildUserPrompt(
		"how is your memory?",
		memory.RetrievalContext{},
		nil,
		"- local memory db healthy: true\n- durable memory items: 4",
		nil,
		false,
		nil,
	)
	if !strings.Contains(memoryPrompt, "Current runtime state:") {
		t.Fatalf("expected runtime state in memory-self prompt, got %q", memoryPrompt)
	}
	if !strings.Contains(memoryPrompt, "If the user asks about your memory, state, or continuity") {
		t.Fatalf("expected state guidance in prompt, got %q", memoryPrompt)
	}

	fetchPrompt := buildUserPrompt(
		"check this https://example.com",
		memory.RetrievalContext{},
		nil,
		"",
		nil,
		false,
		[]fetchedURL{{URL: "https://example.com", Content: "Example page content"}},
	)
	if !strings.Contains(fetchPrompt, "Fetched content from https://example.com") {
		t.Fatalf("expected fetched content in prompt, got %q", fetchPrompt)
	}
	if !strings.Contains(fetchPrompt, "Example page content") {
		t.Fatalf("expected fetched text in prompt, got %q", fetchPrompt)
	}

	nilFetcherService := &Service{}
	if fetched := nilFetcherService.fetchURLs(context.Background(), "check https://example.com"); fetched != nil {
		t.Fatalf("expected nil fetcher to return nil, got %v", fetched)
	}

	noURLService := &Service{fetcher: web.NewFetcher(time.Second)}
	if fetched := noURLService.fetchURLs(context.Background(), "no urls here"); fetched != nil {
		t.Fatalf("expected no URLs to return nil, got %v", fetched)
	}

	fetchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("fetched page content"))
	}))
	defer fetchServer.Close()
	fetchService := &Service{fetcher: web.NewFetcher(5 * time.Second)}
	fetched := fetchService.fetchURLs(context.Background(), "read "+fetchServer.URL+" please")
	if len(fetched) != 1 || fetched[0].Content != "fetched page content" {
		t.Fatalf("unexpected fetchURLs result %#v", fetched)
	}

	badFetchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer badFetchServer.Close()
	badFetched := fetchService.fetchURLs(context.Background(), "read "+badFetchServer.URL+" please")
	if len(badFetched) != 0 {
		t.Fatalf("expected failed fetch to be skipped, got %#v", badFetched)
	}

	if trimCurrentUserEcho("anything", nil) != nil {
		t.Fatal("expected nil return for nil episodes")
	}
	trimmed := trimCurrentUserEcho("oh really?", []memory.Episode{
		{Source: "assistant", Text: "Earlier reply."},
		{Source: "user", Text: "oh really?"},
	})
	if len(trimmed) != 1 || trimmed[0].Source != "assistant" {
		t.Fatalf("unexpected trimmed thread context %#v", trimmed)
	}
	if kept := trimCurrentUserEcho("oh really?", []memory.Episode{{Source: "assistant", Text: "Earlier."}}); len(kept) != 1 {
		t.Fatalf("expected unmatched thread context to be kept, got %#v", kept)
	}
	if kept := excludeThreadEpisodes(nil, "C1", "1.0"); kept != nil {
		t.Fatalf("expected nil episode slice to stay nil, got %#v", kept)
	}

	chatMessages := buildChatMessages("system prompt", "current question", []memory.Episode{
		{Source: "user", Text: "how are you today?"},
		{Source: "assistant", Text: "Steady."},
		{Source: "user", Text: "oh really?"},
	})
	if len(chatMessages) != 5 {
		t.Fatalf("expected 5 chat messages (system + 3 thread + user), got %d", len(chatMessages))
	}
	if chatMessages[0].Role != "system" || chatMessages[1].Role != "user" || chatMessages[2].Role != "assistant" || chatMessages[3].Role != "user" {
		t.Fatalf("unexpected chat message roles %v", chatMessages)
	}
	if chatMessages[4].Content != "current question" {
		t.Fatalf("expected final message to be user prompt, got %q", chatMessages[4].Content)
	}

	emptyMessages := buildChatMessages("system", "question", nil)
	if len(emptyMessages) != 2 {
		t.Fatalf("expected 2 chat messages for empty thread, got %d", len(emptyMessages))
	}

	skippedMessages := buildChatMessages("system", "question", []memory.Episode{
		{Source: "user", Text: ""},
		{Source: "assistant", Summary: "fallback summary"},
	})
	if len(skippedMessages) != 3 {
		t.Fatalf("expected empty text to be skipped or use summary, got %d messages", len(skippedMessages))
	}
	if skippedMessages[1].Content != "fallback summary" {
		t.Fatalf("expected summary fallback, got %q", skippedMessages[1].Content)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
