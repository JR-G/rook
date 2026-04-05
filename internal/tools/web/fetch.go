package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	maxFetchBytes  = 512 * 1024
	maxFetchChars  = 4000
	fetchUserAgent = "rook/0.1"
)

var urlPattern = regexp.MustCompile(`https?://[^\s<>|]+`)

// Fetcher retrieves and extracts readable text from a URL.
type Fetcher struct {
	client *http.Client
}

// NewFetcher builds a URL fetcher with the given timeout.
func NewFetcher(timeout time.Duration) *Fetcher {
	return &Fetcher{
		client: &http.Client{Timeout: timeout},
	}
}

// Fetch retrieves a URL and returns extracted readable text.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", fetchUserAgent)

	response, err := f.client.Do(request)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("fetch %s returned status %d", rawURL, response.StatusCode)
	}

	body := io.LimitReader(response.Body, maxFetchBytes)

	contentType := response.Header.Get("Content-Type")
	if !strings.Contains(contentType, "html") {
		return readPlainBody(body)
	}

	return extractHTMLText(body)
}

func readPlainBody(body io.Reader) (string, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}

	return truncate(strings.TrimSpace(string(raw)), maxFetchChars), nil
}

func extractHTMLText(body io.Reader) (string, error) {
	document, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return "", err
	}

	document.Find("script, style, nav, header, footer, iframe, noscript").Remove()

	text := extractMainContent(document)
	text = collapseWhitespace(text)

	return truncate(text, maxFetchChars), nil
}

func extractMainContent(document *goquery.Document) string {
	if article := document.Find("article, main, [role=main]"); article.Length() > 0 {
		return strings.TrimSpace(article.First().Text())
	}

	return strings.TrimSpace(document.Find("body").Text())
}

// ExtractURLs returns all URLs found in a message string.
func ExtractURLs(text string) []string {
	matches := urlPattern.FindAllString(text, -1)
	seen := make(map[string]struct{}, len(matches))
	unique := make([]string, 0, len(matches))
	for _, match := range matches {
		cleaned := strings.TrimRight(match, ".,;:!?)")
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		unique = append(unique, cleaned)
	}

	return unique
}

// FormatFetchedForPrompt renders fetched URL content for model context.
func FormatFetchedForPrompt(url, content string) string {
	return fmt.Sprintf("Fetched content from %s:\n%s", url, content)
}

func truncate(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}

	return text[:maxChars] + "…"
}

var multiSpacePattern = regexp.MustCompile(`\s+`)

func collapseWhitespace(text string) string {
	return strings.TrimSpace(multiSpacePattern.ReplaceAllString(text, " "))
}
