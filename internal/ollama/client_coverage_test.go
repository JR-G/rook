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

func TestChatRetriesWithoutThink(t *testing.T) {
	t.Parallel()

	requestCount := 0
	client := NewWithHTTPClient("http://ollama.test", time.Second, time.Second, &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requestCount++

			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}

			switch requestCount {
			case 1:
				if payload["think"] != false {
					t.Fatalf("expected first request to disable thinking, got %#v", payload)
				}
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(strings.NewReader("think flag unsupported")),
					Header:     make(http.Header),
				}, nil
			default:
				if _, ok := payload["think"]; ok {
					t.Fatalf("expected retry request to omit think flag, got %#v", payload)
				}
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"model":"qwen3:4b","message":{"content":"ok"}}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})

	result, err := client.ChatStructured(
		context.Background(),
		"qwen3:4b",
		[]Message{{Role: "user", Content: "hi"}},
		0.7,
		map[string]any{"type": "object"},
	)
	if err != nil {
		t.Fatalf("chat retry failed: %v", err)
	}
	if requestCount != 2 || result.Content != "ok" {
		t.Fatalf("unexpected retry result %#v after %d requests", result, requestCount)
	}
}

func TestEmbedLegacyPathAndDoJSONBranches(t *testing.T) {
	t.Parallel()

	client := NewWithHTTPClient("http://ollama.test", time.Second, time.Second, &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.Path {
			case testEmbedEndpoint:
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"embeddings":[]}`)),
					Header:     make(http.Header),
				}, nil
			case testEmbeddingsEndpoint:
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"embedding":[1,2,3]}`)),
					Header:     make(http.Header),
				}, nil
			case "/api/no-content":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("ignored")),
					Header:     make(http.Header),
				}, nil
			case "/api/bad-json":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("{")),
					Header:     make(http.Header),
				}, nil
			default:
				t.Fatalf("unexpected path %s", request.URL.Path)
				return nil, nil
			}
		}),
	})

	embedding, err := client.Embed(context.Background(), "nomic-embed-text", "hello")
	if err != nil {
		t.Fatalf("embed legacy fallback failed: %v", err)
	}
	if len(embedding) != 3 {
		t.Fatalf("unexpected embedding %#v", embedding)
	}

	if err := client.doJSON(context.Background(), http.MethodGet, "/api/no-content", nil, nil); err != nil {
		t.Fatalf("unexpected nil-target error: %v", err)
	}

	var payload struct{}
	if err := client.doJSON(context.Background(), http.MethodGet, "/api/bad-json", nil, &payload); err == nil {
		t.Fatal("expected decode error from malformed json")
	}

	badClient := NewWithHTTPClient("http:// bad host", time.Second, time.Second, &http.Client{})
	if err := badClient.doJSON(context.Background(), http.MethodGet, testTagsEndpoint, nil, nil); err == nil {
		t.Fatal("expected invalid request URL to fail")
	}
}

func TestEmbedReturnsLegacyErrorWhenPrimaryReturnsEmpty(t *testing.T) {
	t.Parallel()

	client := NewWithHTTPClient("http://ollama.test", time.Second, time.Second, &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.Path {
			case testEmbedEndpoint:
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"embeddings":[]}`)),
					Header:     make(http.Header),
				}, nil
			case testEmbeddingsEndpoint:
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(strings.NewReader("legacy unavailable")),
					Header:     make(http.Header),
				}, nil
			default:
				t.Fatalf("unexpected path %s", request.URL.Path)
				return nil, nil
			}
		}),
	})

	if _, err := client.Embed(context.Background(), "nomic-embed-text", "hello"); err == nil {
		t.Fatal("expected legacy embed failure to be returned")
	}
}
