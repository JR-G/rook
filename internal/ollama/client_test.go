package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestChat(t *testing.T) {
	t.Parallel()

	client := NewWithHTTPClient("http://ollama.test", time.Second, time.Second, &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/api/chat" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			body, _ := json.Marshal(map[string]any{
				"model": "phi4-mini",
				"message": map[string]any{
					"content": "hello",
				},
			})
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(string(body))),
				Header:     make(http.Header),
			}, nil
		}),
	})
	result, err := client.Chat(context.Background(), "phi4-mini", []Message{{Role: "user", Content: "hi"}}, 0.2)
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	if result.Content != "hello" {
		t.Fatalf("unexpected content %q", result.Content)
	}
}

func TestChatStructured(t *testing.T) {
	t.Parallel()

	client := NewWithHTTPClient("http://ollama.test", time.Second, time.Second, &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if payload["format"] == nil {
				t.Fatal("expected structured chat payload to include format")
			}

			body, _ := json.Marshal(map[string]any{
				"model": "phi4-mini",
				"message": map[string]any{
					"content": `{"answer":"hello"}`,
				},
			})
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(string(body))),
				Header:     make(http.Header),
			}, nil
		}),
	})

	result, err := client.ChatStructured(
		context.Background(),
		"phi4-mini",
		[]Message{{Role: "user", Content: "hi"}},
		0.2,
		map[string]any{"type": "object"},
	)
	if err != nil {
		t.Fatalf("structured chat failed: %v", err)
	}
	if result.Content != `{"answer":"hello"}` {
		t.Fatalf("unexpected content %q", result.Content)
	}
}

func TestEmbedFallback(t *testing.T) {
	t.Parallel()

	client := NewWithHTTPClient("http://ollama.test", time.Second, time.Second, &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/api/embed":
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("")),
					Header:     make(http.Header),
				}, nil
			case "/api/embeddings":
				body, _ := json.Marshal(map[string]any{
					"embedding": []float64{1, 2, 3},
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
	})
	embedding, err := client.Embed(context.Background(), "nomic-embed-text", "hello")
	if err != nil {
		t.Fatalf("embed failed: %v", err)
	}
	if len(embedding) != 3 {
		t.Fatalf("unexpected embedding length %d", len(embedding))
	}
}

func TestHealth(t *testing.T) {
	t.Parallel()

	client := NewWithHTTPClient("http://ollama.test", time.Second, time.Second, &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, _ := json.Marshal(map[string]any{
				"models": []map[string]any{
					{"name": "phi4-mini"},
				},
			})
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(string(body))),
				Header:     make(http.Header),
			}, nil
		}),
	})
	health, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("health failed: %v", err)
	}
	if !health.Reachable || len(health.Models) != 1 {
		t.Fatalf("unexpected health %#v", health)
	}
}

func TestJoinURLAndNew(t *testing.T) {
	t.Parallel()

	client := New("http://ollama.test", time.Second, time.Second)
	if client == nil {
		t.Fatal("expected client")
	}
	if got := joinURL("http://ollama.test/api", "/chat"); got != "http://ollama.test/api/chat" {
		t.Fatalf("unexpected joined url %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
