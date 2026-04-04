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

func TestRespondWithoutWebStoresDurableMemory(t *testing.T) {
	t.Parallel()

	service, store := newAgentTestService(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/chat":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"model":"qwen3:4b","message":{"content":"{\"answer\":\"plain reply\"}"}}`)),
				Header:     make(http.Header),
			}, nil
		case "/api/embed":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[1,0]]}`)),
				Header:     make(http.Header),
			}, nil
		default:
			return nil, errors.New("unexpected path")
		}
	}))

	response, err := service.Respond(context.Background(), Request{
		ChannelID: "D1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Text:      "remember that my preferred editor is vim",
	})
	if err != nil {
		t.Fatalf("respond failed: %v", err)
	}
	if response.UsedWeb || response.Text != "plain reply" {
		t.Fatalf("unexpected response %#v", response)
	}

	items, err := store.ListRecentMemories(context.Background(), 10)
	if err != nil || len(items) == 0 {
		t.Fatalf("expected durable memory to be stored, got %#v err=%v", items, err)
	}
}

func TestRespondErrorAndNoticeBranches(t *testing.T) {
	t.Parallel()

	service, _ := newAgentTestService(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/chat":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"model":"qwen3:4b","message":{"content":"Already noted.\n\nLive web lookup used."}}`)),
				Header:     make(http.Header),
			}, nil
		case "/api/embed":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[1,0]]}`)),
				Header:     make(http.Header),
			}, nil
		default:
			return nil, errors.New("unexpected path")
		}
	}))
	service.searcher = stubSearcher{results: []web.Result{{Title: "A", URL: "https://example.com"}}}

	response, err := service.Respond(context.Background(), Request{
		ChannelID: "C1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Text:      "what is the latest rook release?",
	})
	if err != nil {
		t.Fatalf("respond with web failed: %v", err)
	}
	if strings.Count(response.Text, "Live web lookup used.") != 1 {
		t.Fatalf("expected one web notice, got %q", response.Text)
	}

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
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	badService := New(
		ollama.NewWithHTTPClient("http://ollama.test", time.Second, time.Second, &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"model":"qwen3:4b","message":{"content":"ok"}}`)),
					Header:     make(http.Header),
				}, nil
			}),
		}),
		store,
		personaManager,
		web.NoopSearcher{},
		output.New(),
		Config{ChatModel: "qwen3:4b", EmbeddingModel: "nomic-embed-text"},
	)
	if _, err := badService.Respond(context.Background(), Request{ChannelID: "D1", ThreadTS: "1.0", UserID: "U1", Text: "hello"}); err == nil {
		t.Fatal("expected closed-store respond to fail")
	}
}
