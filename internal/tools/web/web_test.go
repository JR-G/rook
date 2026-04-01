package web

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDuckDuckGoSearcherParsesResults(t *testing.T) {
	t.Parallel()

	searcher := NewDuckDuckGoSearcher(time.Second, "rook-test")
	searcher.baseURL = "https://duckduckgo.test/html/"
	searcher.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`
			<html><body>
				<div class="result">
					<div class="result__title"><a class="result__a" href="https://example.com/a">Result A</a></div>
					<div class="result__snippet">Snippet A</div>
				</div>
			</body></html>
		`)),
				Header: make(http.Header),
			}, nil
		}),
	}

	results, err := searcher.Search(context.Background(), "rook", 3)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "Result A" {
		t.Fatalf("unexpected title %q", results[0].Title)
	}
}

func TestFormatForPrompt(t *testing.T) {
	t.Parallel()

	formatted := FormatForPrompt([]Result{{Title: "A", URL: "https://example.com", Snippet: "hello"}})
	if formatted == "" {
		t.Fatal("expected prompt formatting")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
