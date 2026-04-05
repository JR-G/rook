package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchExtractsHTMLContent(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Test</title></head><body>
			<nav>skip</nav>
			<article><p>This is the main content.</p></article>
			<footer>skip</footer>
		</body></html>`))
	}))
	defer server.Close()

	fetcher := NewFetcher(5 * time.Second)
	content, err := fetcher.Fetch(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(content, "main content") {
		t.Fatalf("expected article content, got %q", content)
	}
	if strings.Contains(content, "skip") {
		t.Fatalf("expected nav/footer to be stripped, got %q", content)
	}
}

func TestFetchPlainText(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain text content"))
	}))
	defer server.Close()

	fetcher := NewFetcher(5 * time.Second)
	content, err := fetcher.Fetch(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if content != "plain text content" {
		t.Fatalf("expected plain text, got %q", content)
	}
}

func TestFetchTruncatesLongContent(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", maxFetchChars+100)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(long))
	}))
	defer server.Close()

	fetcher := NewFetcher(5 * time.Second)
	content, err := fetcher.Fetch(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.HasSuffix(content, "…") {
		t.Fatalf("expected truncation marker, got len=%d", len(content))
	}
}

func TestFetchReturnsErrorOnBadStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	fetcher := NewFetcher(5 * time.Second)
	if _, err := fetcher.Fetch(context.Background(), server.URL); err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestExtractURLs(t *testing.T) {
	t.Parallel()

	urls := ExtractURLs("check https://example.com and http://foo.bar/baz. also https://example.com again")
	if len(urls) != 2 {
		t.Fatalf("expected 2 unique URLs, got %v", urls)
	}
	if urls[0] != "https://example.com" || urls[1] != "http://foo.bar/baz" {
		t.Fatalf("unexpected URLs %v", urls)
	}

	if len(ExtractURLs("no urls here")) != 0 {
		t.Fatal("expected no URLs")
	}
}

func TestFormatFetchedForPrompt(t *testing.T) {
	t.Parallel()

	formatted := FormatFetchedForPrompt("https://example.com", "some content")
	if !strings.Contains(formatted, "https://example.com") || !strings.Contains(formatted, "some content") {
		t.Fatalf("unexpected format %q", formatted)
	}
}

func TestFetchFallsBackToBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><p>Body content only.</p></body></html>`))
	}))
	defer server.Close()

	fetcher := NewFetcher(5 * time.Second)
	content, err := fetcher.Fetch(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(content, "Body content only") {
		t.Fatalf("expected body fallback, got %q", content)
	}
}
