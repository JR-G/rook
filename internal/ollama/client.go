package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Message is an Ollama chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResult is a normalised chat response.
type ChatResult struct {
	Model   string
	Content string
}

// Health describes current Ollama reachability.
type Health struct {
	Reachable bool
	Models    []string
}

// StatusError reports an HTTP-level Ollama API failure.
type StatusError struct {
	StatusCode int
	Message    string
}

// Error implements the error interface.
func (e StatusError) Error() string {
	if strings.TrimSpace(e.Message) == "" {
		return fmt.Sprintf("ollama returned status %d", e.StatusCode)
	}

	return fmt.Sprintf("ollama returned status %d: %s", e.StatusCode, e.Message)
}

// Client wraps the Ollama HTTP API.
type Client struct {
	host         string
	httpClient   *http.Client
	chatTimeout  time.Duration
	embedTimeout time.Duration
}

// New creates a new Ollama client.
func New(host string, chatTimeout, embedTimeout time.Duration) *Client {
	return NewWithHTTPClient(host, chatTimeout, embedTimeout, &http.Client{
		Timeout: chatTimeout,
	})
}

// NewWithHTTPClient creates a new Ollama client with a custom HTTP client.
func NewWithHTTPClient(host string, chatTimeout, embedTimeout time.Duration, httpClient *http.Client) *Client {
	return &Client{
		host:         strings.TrimRight(host, "/"),
		httpClient:   httpClient,
		chatTimeout:  chatTimeout,
		embedTimeout: embedTimeout,
	}
}

// Chat sends a non-streaming chat request.
func (c *Client) Chat(ctx context.Context, model string, messages []Message, temperature float64) (ChatResult, error) {
	requestCtx, cancel := context.WithTimeout(ctx, c.chatTimeout)
	defer cancel()

	response, err := c.chatOnce(requestCtx, model, messages, temperature, true)
	if err == nil {
		return response, nil
	}
	if !shouldRetryWithoutThink(model, err) {
		return ChatResult{}, err
	}

	return c.chatOnce(requestCtx, model, messages, temperature, false)
}

// ShouldFallbackModel reports whether another local chat model should be tried.
func ShouldFallbackModel(err error) bool {
	var statusErr StatusError
	if !errors.As(err, &statusErr) {
		return false
	}

	return statusErr.StatusCode == http.StatusBadRequest || statusErr.StatusCode == http.StatusNotFound
}

func chatPayload(model string, messages []Message, temperature float64, disableThinking bool) map[string]any {
	payload := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   false,
		"options":  modelOptions(model, temperature),
	}

	if disableThinking && usesThinkingToggle(model) {
		payload["think"] = false
	}

	return payload
}

func (c *Client) chatOnce(
	ctx context.Context,
	model string,
	messages []Message,
	temperature float64,
	disableThinking bool,
) (ChatResult, error) {
	var response struct {
		Model   string `json:"model"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}

	err := c.doJSON(ctx, http.MethodPost, "/api/chat", chatPayload(model, messages, temperature, disableThinking), &response)
	if err != nil {
		return ChatResult{}, err
	}

	return ChatResult{
		Model:   response.Model,
		Content: strings.TrimSpace(response.Message.Content),
	}, nil
}

func modelOptions(model string, temperature float64) map[string]any {
	options := map[string]any{
		"temperature": temperature,
	}

	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "qwen3") {
		return options
	}

	options["top_k"] = 20
	options["top_p"] = 0.8

	return options
}

func shouldRetryWithoutThink(model string, err error) bool {
	if !usesThinkingToggle(model) {
		return false
	}

	var statusErr StatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusBadRequest
}

func usesThinkingToggle(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "qwen3")
}

// Embed generates an embedding for a single input.
func (c *Client) Embed(ctx context.Context, model, input string) ([]float64, error) {
	requestCtx, cancel := context.WithTimeout(ctx, c.embedTimeout)
	defer cancel()

	var embedResponse struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	err := c.doJSON(requestCtx, http.MethodPost, "/api/embed", map[string]any{
		"model": model,
		"input": input,
	}, &embedResponse)
	if err == nil && len(embedResponse.Embeddings) > 0 {
		return embedResponse.Embeddings[0], nil
	}

	var legacyResponse struct {
		Embedding []float64 `json:"embedding"`
	}
	legacyErr := c.doJSON(requestCtx, http.MethodPost, "/api/embeddings", map[string]any{
		"model":  model,
		"prompt": input,
	}, &legacyResponse)
	if legacyErr == nil {
		return legacyResponse.Embedding, nil
	}
	if err != nil {
		return nil, err
	}

	return nil, legacyErr
}

// Health checks local reachability and lists installed models when possible.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var response struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	if err := c.doJSON(ctx, http.MethodGet, "/api/tags", nil, &response); err != nil {
		return Health{Reachable: false}, err
	}

	models := make([]string, 0, len(response.Models))
	for _, model := range response.Models {
		models = append(models, model.Name)
	}

	return Health{
		Reachable: true,
		Models:    models,
	}, nil
}

func (c *Client) doJSON(ctx context.Context, method, endpoint string, payload, target any) error {
	bodyReader, err := newBodyReader(payload)
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, method, joinURL(c.host, endpoint), bodyReader)
	if err != nil {
		return err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode >= http.StatusBadRequest {
		return readStatusError(response)
	}

	if target == nil {
		return nil
	}

	return json.NewDecoder(response.Body).Decode(target)
}

func newBodyReader(payload any) (*bytes.Reader, error) {
	if payload == nil {
		return bytes.NewReader(nil), nil
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return bytes.NewReader(body), nil
}

func readStatusError(response *http.Response) error {
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return StatusError{StatusCode: response.StatusCode}
	}

	return StatusError{
		StatusCode: response.StatusCode,
		Message:    strings.TrimSpace(string(body)),
	}
}

func joinURL(baseURL, endpoint string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return baseURL + endpoint
	}

	parsed.Path = path.Join(parsed.Path, endpoint)

	return parsed.String()
}
