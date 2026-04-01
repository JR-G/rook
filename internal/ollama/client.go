package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// Client wraps the Ollama HTTP API.
type Client struct {
	host         string
	httpClient   *http.Client
	chatTimeout  time.Duration
	embedTimeout time.Duration
}

// New creates a new Ollama client.
func New(host string, chatTimeout, embedTimeout time.Duration) *Client {
	return &Client{
		host: strings.TrimRight(host, "/"),
		httpClient: &http.Client{
			Timeout: chatTimeout,
		},
		chatTimeout:  chatTimeout,
		embedTimeout: embedTimeout,
	}
}

// Chat sends a non-streaming chat request.
func (c *Client) Chat(ctx context.Context, model string, messages []Message, temperature float64) (ChatResult, error) {
	payload := map[string]any{
		"model":       model,
		"messages":    messages,
		"stream":      false,
		"temperature": temperature,
	}

	var response struct {
		Model   string `json:"model"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}

	requestCtx, cancel := context.WithTimeout(ctx, c.chatTimeout)
	defer cancel()

	if err := c.doJSON(requestCtx, http.MethodPost, "/api/chat", payload, &response); err != nil {
		return ChatResult{}, err
	}

	return ChatResult{
		Model:   response.Model,
		Content: strings.TrimSpace(response.Message.Content),
	}, nil
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
	if legacyErr := c.doJSON(requestCtx, http.MethodPost, "/api/embeddings", map[string]any{
		"model":  model,
		"prompt": input,
	}, &legacyResponse); legacyErr != nil {
		if err != nil {
			return nil, err
		}

		return nil, legacyErr
	}

	return legacyResponse.Embedding, nil
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

func (c *Client) doJSON(ctx context.Context, method, endpoint string, payload any, target any) error {
	var bodyReader *bytes.Reader
	if payload == nil {
		bodyReader = bytes.NewReader(nil)
	} else {
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(body)
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
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("ollama returned status %d", response.StatusCode)
	}

	if target == nil {
		return nil
	}

	return json.NewDecoder(response.Body).Decode(target)
}

func joinURL(baseURL, endpoint string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return baseURL + endpoint
	}

	parsed.Path = path.Join(parsed.Path, endpoint)

	return parsed.String()
}
