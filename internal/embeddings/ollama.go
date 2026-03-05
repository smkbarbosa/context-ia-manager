package embeddings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client calls the Ollama API to generate text embeddings.
type Client struct {
	baseURL string
	model   string
	http    *http.Client
}

// embedRequest uses the current Ollama /api/embed endpoint.
// Input accepts a single string or a slice of strings.
// Truncate instructs Ollama to silently trim inputs that exceed the model's
// context window instead of returning an error.
type embedRequest struct {
	Model    string `json:"model"`
	Input    any    `json:"input"`    // string or []string
	Truncate bool   `json:"truncate"` // always true — avoids "input length exceeds context" errors
}

// embedResponse matches the /api/embed response format.
type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// New creates a new Ollama embeddings client.
func New(ollamaURL, model string) *Client {
	return &Client{
		baseURL: ollamaURL,
		model:   model,
		http:    &http.Client{Timeout: 5 * time.Minute},
	}
}

// Embed returns a vector embedding for the given text.
func (c *Client) Embed(text string) ([]float32, error) {
	body, err := json.Marshal(embedRequest{Model: c.model, Input: text, Truncate: true})
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Post(c.baseURL+"/api/embed", "application/json",
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var er embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, err
	}
	if len(er.Embeddings) == 0 {
		return nil, fmt.Errorf("ollama returned empty embeddings")
	}
	return er.Embeddings[0], nil
}

// EmbedBatch generates embeddings for all texts in a single Ollama call.
// Ollama /api/embed accepts an array for "input" and returns all embeddings at once,
// which is far more efficient than sending one request per text.
func (c *Client) EmbedBatch(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	body, err := json.Marshal(embedRequest{Model: c.model, Input: texts, Truncate: true})
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Post(c.baseURL+"/api/embed", "application/json",
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var er embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, err
	}
	if len(er.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama returned %d embeddings, expected %d",
			len(er.Embeddings), len(texts))
	}
	return er.Embeddings, nil
}
