package embeddings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// Client calls the Ollama API to generate text embeddings.
type Client struct {
	baseURL string
	model   string
	http    *http.Client
}

type embedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type embedResponse struct {
	Embedding []float32 `json:"embedding"`
}

// New creates a new Ollama embeddings client.
func New(ollamaURL, model string) *Client {
	return &Client{
		baseURL: ollamaURL,
		model:   model,
		http:    &http.Client{},
	}
}

// Embed returns a vector embedding for the given text.
func (c *Client) Embed(text string) ([]float32, error) {
	body, err := json.Marshal(embedRequest{Model: c.model, Prompt: text})
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Post(c.baseURL+"/api/embeddings", "application/json",
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
	return er.Embedding, nil
}

// EmbedBatch generates embeddings for multiple texts concurrently.
func (c *Client) EmbedBatch(texts []string) ([][]float32, error) {
	type result struct {
		idx int
		vec []float32
		err error
	}

	ch := make(chan result, len(texts))

	for i, text := range texts {
		go func(idx int, t string) {
			vec, err := c.Embed(t)
			ch <- result{idx: idx, vec: vec, err: err}
		}(i, text)
	}

	vectors := make([][]float32, len(texts))
	for range texts {
		r := <-ch
		if r.err != nil {
			return nil, r.err
		}
		vectors[r.idx] = r.vec
	}
	return vectors, nil
}
