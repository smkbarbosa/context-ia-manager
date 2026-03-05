package embeddings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/smkbarbosa/context-ia-manager/internal/cache"
)

// Client calls the Ollama API to generate text embeddings.
type Client struct {
	baseURL    string
	model      string
	http       *http.Client
	embedCache *cache.EmbedCache
}

// EmbedCacheLen retorna o número de entradas no cache de embeddings (útil para métricas).
func (c *Client) EmbedCacheLen() int { return c.embedCache.Len() }

// EmbedCacheHits retorna o total de cache hits acumulados.
func (c *Client) EmbedCacheHits() int64 { return c.embedCache.Hits() }

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
		baseURL:    ollamaURL,
		model:      model,
		http:       &http.Client{Timeout: 5 * time.Minute},
		embedCache: cache.NewEmbedCache(2000),
	}
}

// Embed returns a vector embedding for the given text.
func (c *Client) Embed(text string) ([]float32, error) {
	// L1: verifica cache antes de chamar o Ollama.
	if vec, ok := c.embedCache.Get(text); ok {
		return vec, nil
	}

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
	vec := er.Embeddings[0]
	c.embedCache.Set(text, vec)
	return vec, nil
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
