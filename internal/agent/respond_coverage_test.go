package agent

import (
	"context"
	"encoding/json"
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

func TestRespondHandlesEmbedFailureAndChatFailure(t *testing.T) {
	t.Parallel()

	service, _ := newAgentTestService(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case testAgentEmbedEndpoint:
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader("embed failed")),
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
	response, err := service.Respond(context.Background(), Request{
		ChannelID: "D1",
		ThreadTS:  "1.0",
		UserID:    "U1",
		Text:      "hello",
	})
	if err != nil || response.Text != "ok" {
		t.Fatalf("unexpected embed-failure response %#v err=%v", response, err)
	}

	failingService, _ := newAgentTestService(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case testAgentEmbedEndpoint:
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[1,0]]}`)),
				Header:     make(http.Header),
			}, nil
		case testAgentChatEndpoint:
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader("chat failed")),
				Header:     make(http.Header),
			}, nil
		default:
			return nil, errors.New("unexpected path")
		}
	}))
	if _, err := failingService.Respond(context.Background(), Request{ChannelID: "D1", ThreadTS: "1.0", UserID: "U1", Text: "hello"}); err == nil {
		t.Fatal("expected chat failure to bubble up")
	}
}

func TestRespondFailsWhenPersonaPromptCannotRender(t *testing.T) {
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
	if err := os.Remove(corePath); err != nil {
		t.Fatalf("remove core file: %v", err)
	}

	service := New(
		ollama.NewWithHTTPClient("http://ollama.test", time.Second, time.Second, &http.Client{
			Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				switch request.URL.Path {
				case testAgentEmbedEndpoint:
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[1,0]]}`)),
						Header:     make(http.Header),
					}, nil
				default:
					return nil, errors.New("unexpected path")
				}
			}),
		}),
		store,
		personaManager,
		web.NoopSearcher{},
		Config{ChatModel: "qwen3:4b", EmbeddingModel: "nomic-embed-text"},
	)
	if _, err := service.Respond(context.Background(), Request{ChannelID: "D1", ThreadTS: "1.0", UserID: "U1", Text: "hello"}); err == nil {
		t.Fatal("expected missing core prompt file to fail")
	}
}

func TestChatWithFallbackSuccessAfterPrimaryModelMiss(t *testing.T) {
	t.Parallel()

	service, _ := newAgentTestService(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != testAgentChatEndpoint {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[1,0]]}`)),
				Header:     make(http.Header),
			}, nil
		}

		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.Model == "qwen3:4b" {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("missing model")),
				Header:     make(http.Header),
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"model":"phi4-mini","message":{"content":"{\"answer\":\"ok\"}"}}`)),
			Header:     make(http.Header),
		}, nil
	}))

	result, err := service.chatWithFallback(
		context.Background(),
		service.snapshot(),
		[]ollama.Message{{Role: "user", Content: "hi"}},
		output.AnswerSchema(),
	)
	if err != nil || result.Model != "phi4-mini" {
		t.Fatalf("unexpected fallback chat result %#v err=%v", result, err)
	}
}
