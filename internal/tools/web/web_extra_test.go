package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const testDuckDuckGoURL = "https://duckduckgo.test/html/"

func TestDuckDuckGoSearcherMetaAndErrors(t *testing.T) {
	t.Parallel()

	searcher := NewDuckDuckGoSearcher(time.Second, "rook-test")
	if !searcher.Enabled() || searcher.Provider() != "duckduckgo" {
		t.Fatalf("unexpected searcher state enabled=%t provider=%q", searcher.Enabled(), searcher.Provider())
	}

	searcher.baseURL = "://bad url"
	if _, err := searcher.Search(context.Background(), "rook", 1); err == nil {
		t.Fatal("expected invalid base url to fail")
	}

	searcher.baseURL = testDuckDuckGoURL
	searcher.client = &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("User-Agent") != "rook-test" {
				t.Fatalf("unexpected user agent %q", request.Header.Get("User-Agent"))
			}
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader("down")),
				Header:     make(http.Header),
			}, nil
		}),
	}
	if _, err := searcher.Search(context.Background(), "rook", 1); err == nil {
		t.Fatal("expected provider status failure")
	}

	searcher.client = &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("<html><body></body></html>")),
				Header:     make(http.Header),
			}, nil
		}),
	}
	if _, err := searcher.Search(context.Background(), "rook", 1); err == nil {
		t.Fatal("expected empty results failure")
	}
}

func TestDuckDuckGoSearcherTransportErrorAndPromptFormatting(t *testing.T) {
	t.Parallel()

	searcher := NewDuckDuckGoSearcher(time.Second, "rook-test")
	searcher.baseURL = testDuckDuckGoURL
	searcher.client = &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network down")
		}),
	}
	if _, err := searcher.Search(context.Background(), "rook", 1); err == nil {
		t.Fatal("expected transport error")
	}

	formatted := FormatForPrompt([]Result{
		{Title: "A", URL: "https://example.com/a"},
		{Title: "B", URL: "https://example.com/b", Snippet: "hello"},
	})
	if !strings.Contains(formatted, "1. A") || !strings.Contains(formatted, "Snippet: hello") {
		t.Fatalf("unexpected prompt format %q", formatted)
	}
}
