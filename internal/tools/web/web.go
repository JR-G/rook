package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Result is a normalised search hit.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// Searcher looks up live web results behind an explicit tool boundary.
type Searcher interface {
	Enabled() bool
	Provider() string
	Search(ctx context.Context, query string, maxResults int) ([]Result, error)
}

// NoopSearcher disables live web access.
type NoopSearcher struct{}

// Enabled reports whether the searcher is active.
func (NoopSearcher) Enabled() bool { return false }

// Provider returns the provider name.
func (NoopSearcher) Provider() string { return "disabled" }

// Search rejects any request because web access is disabled.
func (NoopSearcher) Search(context.Context, string, int) ([]Result, error) {
	return nil, fmt.Errorf("web search is disabled")
}

// DuckDuckGoSearcher provides anonymous HTML search.
type DuckDuckGoSearcher struct {
	baseURL   string
	userAgent string
	client    *http.Client
}

// NewDuckDuckGoSearcher builds a DuckDuckGo-backed searcher.
func NewDuckDuckGoSearcher(timeout time.Duration, userAgent string) *DuckDuckGoSearcher {
	return &DuckDuckGoSearcher{
		baseURL:   "https://html.duckduckgo.com/html/",
		userAgent: userAgent,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Enabled reports that the provider is active.
func (s *DuckDuckGoSearcher) Enabled() bool { return true }

// Provider returns the provider name.
func (s *DuckDuckGoSearcher) Provider() string { return "duckduckgo" }

// Search performs a live web lookup and normalises the result list.
func (s *DuckDuckGoSearcher) Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	requestURL, err := url.Parse(s.baseURL)
	if err != nil {
		return nil, err
	}

	params := requestURL.Query()
	params.Set("q", query)
	requestURL.RawQuery = params.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", s.userAgent)

	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("duckduckgo returned status %d", response.StatusCode)
	}

	document, err := goquery.NewDocumentFromReader(response.Body)
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, maxResults)
	document.Find(".result").EachWithBreak(func(_ int, selection *goquery.Selection) bool {
		if len(results) >= maxResults {
			return false
		}

		titleNode := selection.Find(".result__title .result__a").First()
		title := strings.TrimSpace(titleNode.Text())
		link, _ := titleNode.Attr("href")
		snippet := strings.TrimSpace(selection.Find(".result__snippet").First().Text())

		if title == "" || link == "" {
			return true
		}

		results = append(results, Result{
			Title:   title,
			URL:     link,
			Snippet: snippet,
		})

		return true
	})

	if len(results) == 0 {
		return nil, fmt.Errorf("no search results returned")
	}

	return results, nil
}

// FormatForPrompt renders search results for internal model context.
func FormatForPrompt(results []Result) string {
	var builder strings.Builder
	for idx, result := range results {
		builder.WriteString(fmt.Sprintf("%d. %s\n", idx+1, result.Title))
		builder.WriteString(fmt.Sprintf("URL: %s\n", result.URL))
		if result.Snippet != "" {
			builder.WriteString(fmt.Sprintf("Snippet: %s\n", result.Snippet))
		}
		builder.WriteString("\n")
	}

	return strings.TrimSpace(builder.String())
}
