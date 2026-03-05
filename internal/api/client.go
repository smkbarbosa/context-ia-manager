package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/smkbarbosa/context-ia-manager/internal/storage"
)

// Client is a thin HTTP client for the ciam REST API.
type Client struct {
	base string
	http *http.Client
}

// NewClient creates a new API client pointing at baseURL.
func NewClient(baseURL string) *Client {
	return &Client{base: normaliseURL(baseURL), http: &http.Client{}}
}

// IndexResponse is the result of a successful index operation.
type IndexResponse struct {
	ProjectID      string `json:"project_id"`
	ProjectType    string `json:"project_type"`
	ChunksIndexed  int    `json:"chunks_indexed"`
	FilesProcessed int    `json:"files_processed"`
}

// SearchResponse wraps search results.
type SearchResponse struct {
	Chunks []SearchChunk `json:"chunks"`
}

// SearchChunk is a single search result item.
type SearchChunk struct {
	FilePath  string  `json:"file_path"`
	ChunkType string  `json:"chunk_type"`
	Content   string  `json:"content"`
	Score     float32 `json:"score"`
}

// StatusResponse contains service metrics.
type StatusResponse struct {
	ProjectsIndexed      int `json:"ProjectsIndexed"`
	TotalChunks          int `json:"TotalChunks"`
	MemoriesStored       int `json:"MemoriesStored"`
	CacheHits            int `json:"CacheHits"`
	EstimatedTokensSaved int `json:"EstimatedTokensSaved"`
}

// Index sends an index request to the API.
func (c *Client) Index(projectPath, projectType string) (*IndexResponse, error) {
	var result IndexResponse
	err := c.post("/api/v1/project/index", map[string]any{
		"project_path": projectPath,
		"project_type": projectType,
	}, &result)
	return &result, err
}

// ChunkPayload is a single pre-chunked piece of source code sent by the CLI.
type ChunkPayload struct {
	FilePath  string `json:"file_path"`
	ChunkType string `json:"chunk_type"`
	Content   string `json:"content"`
}

// IndexChunks sends locally-produced chunks to the API for embedding + storage.
// Use this instead of Index when the API runs inside Docker and cannot access
// the host filesystem.
func (c *Client) IndexChunks(projectID, projectType string, chunks []ChunkPayload) (*IndexResponse, error) {
	var result IndexResponse
	err := c.post("/api/v1/project/chunks", map[string]any{
		"project_id":   projectID,
		"project_type": projectType,
		"chunks":       chunks,
	}, &result)
	return &result, err
}

// Search sends a search request to the API.
func (c *Client) Search(query, chunkType string, limit int, compress bool) (*SearchResponse, error) {
	var result SearchResponse
	err := c.post("/api/v1/search", map[string]any{
		"query":      query,
		"chunk_type": chunkType,
		"limit":      limit,
		"compress":   compress,
	}, &result)
	return &result, err
}

// Remember stores a memory entry.
func (c *Client) Remember(content, memType string) error {
	return c.post("/api/v1/memory/store", map[string]any{
		"content": content,
		"type":    memType,
	}, nil)
}

// Recall retrieves memories matching query.
func (c *Client) Recall(query string) ([]storage.Memory, error) {
	var resp struct {
		Memories []storage.Memory `json:"memories"`
	}
	err := c.post("/api/v1/memory/recall", map[string]any{
		"query": query,
		"limit": 5,
	}, &resp)
	return resp.Memories, err
}

// Status returns service metrics.
func (c *Client) Status() (*StatusResponse, error) {
	req, err := http.NewRequest(http.MethodGet, c.base+"/api/v1/status", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result StatusResponse
	return &result, json.NewDecoder(resp.Body).Decode(&result)
}

// post is a generic JSON POST helper.
func (c *Client) post(path string, body any, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	resp, err := c.http.Post(c.base+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp map[string]string
		json.NewDecoder(resp.Body).Decode(&errResp) //nolint:errcheck
		return fmt.Errorf("API error %d: %s", resp.StatusCode, errResp["error"])
	}

	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}
