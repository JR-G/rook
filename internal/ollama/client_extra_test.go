package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/JR-G/rook/internal/failures"
)

const (
	testChatEndpoint       = "/api/chat"
	testEmbedEndpoint      = "/api/embed"
	testEmbeddingsEndpoint = "/api/embeddings"
	testTagsEndpoint       = "/api/tags"
)

type badJSON struct{}

func (badJSON) MarshalJSON() ([]byte, error) {
	return nil, errors.New("marshal failed")
}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingReadCloser) Close() error             { return nil }

func TestOllamaHelperFunctions(t *testing.T) {
	t.Parallel()

	if got := (StatusError{StatusCode: http.StatusNotFound}).Error(); got != "ollama returned status 404" {
		t.Fatalf("unexpected status error %q", got)
	}
	if got := (StatusError{StatusCode: http.StatusBadRequest, Message: "model missing"}).Error(); !strings.Contains(got, "model missing") {
		t.Fatalf("unexpected status error message %q", got)
	}

	if !ShouldFallbackModel(StatusError{StatusCode: http.StatusBadRequest}) {
		t.Fatal("expected 400 to be fallback-eligible")
	}
	if ShouldFallbackModel(errors.New("boom")) {
		t.Fatal("did not expect plain error to trigger fallback")
	}
	payload := chatPayload("gemma4:e4b", []Message{{Role: "user", Content: "hi"}}, 0.7, map[string]any{"type": "object"})
	if payload["format"] == nil {
		t.Fatalf("expected structured format payload, got %#v", payload)
	}
	opts, ok := payload["options"].(map[string]any)
	if !ok || opts["temperature"] != 0.7 {
		t.Fatalf("expected temperature in options, got %#v", payload)
	}
	plainPayload := chatPayload("gemma4:e4b", []Message{{Role: "user", Content: "hi"}}, 0.5, nil)
	if plainPayload["format"] != nil {
		t.Fatalf("expected no format for nil schema, got %#v", plainPayload)
	}
}

func TestNewBodyReaderAndStatusParsing(t *testing.T) {
	t.Parallel()

	reader, err := newBodyReader(nil)
	if err != nil || reader.Len() != 0 {
		t.Fatalf("unexpected nil payload reader: len=%d err=%v", reader.Len(), err)
	}
	if _, err := newBodyReader(badJSON{}); err == nil {
		t.Fatal("expected marshal failure")
	}

	if got := joinURL("://bad base", "/chat"); got != "://bad base/chat" {
		t.Fatalf("unexpected fallback join url %q", got)
	}

	err = readStatusError(&http.Response{
		StatusCode: http.StatusBadGateway,
		Body:       io.NopCloser(strings.NewReader(" upstream failed ")),
	})
	if err == nil || !strings.Contains(err.Error(), "upstream failed") {
		t.Fatalf("unexpected status parse error %v", err)
	}

	err = readStatusError(&http.Response{
		StatusCode: http.StatusBadGateway,
		Body:       failingReadCloser{},
	})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("unexpected fallback status parse error %v", err)
	}

	err = readStatusError(&http.Response{
		StatusCode: http.StatusInternalServerError,
		Body: io.NopCloser(strings.NewReader(`time=2026-04-04T14:43:14.263+01:00 level=INFO source=server.go:1384 msg="waiting for server to become available" status="llm server loading model"
[GIN] 2026/04/04 - 14:44:43 | 500 | 1m30s | 127.0.0.1 | POST "/api/chat"`)),
	})
	if err == nil || !strings.Contains(err.Error(), "llm server loading model") {
		t.Fatalf("unexpected sanitised status error %v", err)
	}
}

func TestChatAndEmbedErrorPaths(t *testing.T) {
	t.Parallel()

	requestCount := 0
	client := NewWithHTTPClient("http://ollama.test", time.Second, time.Second, &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requestCount++
			if request.URL.Path == testChatEndpoint {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(strings.NewReader("slow down")),
					Header:     make(http.Header),
				}, nil
			}
			return nil, errors.New("unexpected path")
		}),
	})

	if _, err := client.Chat(context.Background(), "gemma4:e4b", []Message{{Role: "user", Content: "hi"}}, 0.7); err == nil {
		t.Fatal("expected chat to fail")
	}
	if requestCount != 1 {
		t.Fatalf("expected one chat attempt, got %d", requestCount)
	}

	embedClient := NewWithHTTPClient("http://ollama.test", time.Second, time.Second, &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.Path {
			case testEmbedEndpoint:
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(strings.NewReader("embed failed")),
					Header:     make(http.Header),
				}, nil
			case testEmbeddingsEndpoint:
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(strings.NewReader("legacy failed")),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, errors.New("unexpected path")
			}
		}),
	})

	_, err := embedClient.Embed(context.Background(), "nomic-embed-text", "hello")
	if err == nil || !strings.Contains(err.Error(), "embed failed") {
		t.Fatalf("expected primary embed error, got %v", err)
	}
}

func TestHealthErrorAndSuccessfulChatPayloadInspection(t *testing.T) {
	t.Parallel()

	requests := make([]map[string]any, 0, 1)
	client := NewWithHTTPClient("http://ollama.test", time.Second, time.Second, &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.Path {
			case testChatEndpoint:
				var payload map[string]any
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatalf("decode payload: %v", err)
				}
				requests = append(requests, payload)
				body, _ := json.Marshal(map[string]any{
					"model": "gemma4:e4b",
					"message": map[string]any{
						"content": "ok",
					},
				})
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(body))),
					Header:     make(http.Header),
				}, nil
			case testTagsEndpoint:
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(strings.NewReader("down")),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, errors.New("unexpected path")
			}
		}),
	})

	result, err := client.ChatStructured(context.Background(), "gemma4:e4b", []Message{{Role: "user", Content: "hi"}}, 0.7, map[string]any{"type": "object"})
	if err != nil || result.Content != "ok" {
		t.Fatalf("unexpected chat result %#v err=%v", result, err)
	}
	if len(requests) != 1 || requests[0]["format"] == nil {
		t.Fatalf("unexpected chat requests %#v", requests)
	}

	if _, err := client.Health(context.Background()); err == nil {
		t.Fatal("expected health failure")
	}
}

func TestWrapUserVisibleMappings(t *testing.T) {
	t.Parallel()

	if got := failures.Message(WrapUserVisible(context.DeadlineExceeded)); !strings.Contains(got, "warming up") {
		t.Fatalf("unexpected deadline message %q", got)
	}
	if got := failures.Message(WrapUserVisible(StatusError{StatusCode: http.StatusInternalServerError, Message: "llm server loading model"})); !strings.Contains(got, "warming up") {
		t.Fatalf("unexpected loading-model message %q", got)
	}
	if got := failures.Message(WrapUserVisible(StatusError{StatusCode: http.StatusTooManyRequests, Message: "busy"})); !strings.Contains(got, "busy") {
		t.Fatalf("unexpected busy message %q", got)
	}
	if got := failures.Message(WrapUserVisible(StatusError{StatusCode: http.StatusInternalServerError, Message: "boom"})); !strings.Contains(got, "failed while generating") {
		t.Fatalf("unexpected internal-server message %q", got)
	}
	if got := failures.Message(WrapUserVisible(errors.New("plain"))); got != "" {
		t.Fatalf("did not expect plain error to become user-visible, got %q", got)
	}
	if got := failures.Message(WrapUserVisible(context.Canceled)); !strings.Contains(got, "stopped") {
		t.Fatalf("expected canceled message, got %q", got)
	}
	if got := failures.Message(WrapUserVisible(nil)); got != "" {
		t.Fatalf("expected empty message for nil error, got %q", got)
	}
	if got := failures.Message(WrapUserVisible(StatusError{StatusCode: http.StatusBadRequest, Message: "unknown error"})); got != "" {
		t.Fatalf("expected empty for unrecognised status error, got %q", got)
	}
}
