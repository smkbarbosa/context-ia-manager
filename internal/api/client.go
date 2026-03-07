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

// MCPToolStat mirrors storage.MCPToolStat for the CLI layer.
type MCPToolStat struct {
	ToolName     string   `json:"tool_name"`
	CallCount    int64    `json:"call_count"`
	ErrorCount   int64    `json:"error_count"`
	AvgLatencyMs float64  `json:"avg_latency_ms"`
	LastCalledAt string   `json:"last_called_at"`
	TopQueries   []string `json:"top_queries,omitempty"`
}

// ProjectMetric mirrors storage.ProjectMetric for the CLI layer.
type ProjectMetric struct {
	ProjectID     string         `json:"project_id"`
	TotalChunks   int            `json:"total_chunks"`
	TotalFiles    int            `json:"total_files"`
	TokenEstimate int64          `json:"token_estimate"`
	LastIndexed   string         `json:"last_indexed"`
	ChunkTypes    map[string]int `json:"chunk_types"`
}

// StatusResponse contains service metrics.
type StatusResponse struct {
	ProjectsIndexed       int             `json:"projects_indexed"`
	TotalChunks           int             `json:"total_chunks"`
	MemoriesStored        int             `json:"memories_stored"`
	CacheHits             int             `json:"cache_hits"`
	EstimatedTokensSaved  int             `json:"estimated_tokens_saved"`
	TokensServedViaSearch int64           `json:"tokens_served_via_search"`
	TotalProjectTokens    int64           `json:"total_project_tokens"`
	Projects              []ProjectMetric `json:"projects,omitempty"`
	MCPStats              []MCPToolStat   `json:"mcp_tools,omitempty"`
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
// Set reset=true on the first batch so the server clears stale data before
// appending; subsequent batches must pass reset=false.
func (c *Client) IndexChunks(projectID, projectType string, chunks []ChunkPayload, reset bool) (*IndexResponse, error) {
	var result IndexResponse
	err := c.post("/api/v1/project/chunks", map[string]any{
		"project_id":   projectID,
		"project_type": projectType,
		"reset":        reset,
		"chunks":       chunks,
	}, &result)
	return &result, err
}

// FileIndexResponse is the result of a per-file incremental index.
type FileIndexResponse struct {
	ProjectID     string `json:"project_id"`
	FilePath      string `json:"file_path"`
	Updated       bool   `json:"updated"`
	ChunksIndexed int    `json:"chunks_indexed"`
}

// IndexFile sends a single file's chunks for incremental indexing.
// fileHash is the SHA-256 hex of the file content; the server skips
// embedding if the hash matches the stored one.
func (c *Client) IndexFile(projectID, fileHash string, chunks []ChunkPayload) (*FileIndexResponse, error) {
	var result FileIndexResponse
	err := c.post("/api/v1/project/file", map[string]any{
		"project_id": projectID,
		"file_hash":  fileHash,
		"chunks":     chunks,
	}, &result)
	return &result, err
}

// Search sends a search request to the API.
// projectID must match the ID used during indexing (typically filepath.Base of the project root).
func (c *Client) Search(query, projectID, chunkType string, limit int, compress bool) (*SearchResponse, error) {
	var result SearchResponse
	err := c.post("/api/v1/search", map[string]any{
		"query":      query,
		"project_id": projectID,
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

// RecordTool fires a tool usage metric to the API (fire-and-forget friendly).
func (c *Client) RecordTool(tool, query string, latencyMs int64, isError bool) error {
	return c.post("/api/v1/metrics/tool", map[string]any{
		"tool":       tool,
		"query":      query,
		"latency_ms": latencyMs,
		"is_error":   isError,
	}, nil)
}

// DraftRequest is the payload for /api/v1/draft.
type DraftRequest struct {
	ProjectID string `json:"project_id"`
	Intent    string `json:"intent"`
	ChunkType string `json:"chunk_type,omitempty"`
	PlanID    string `json:"plan_id,omitempty"`
	Phase     string `json:"phase,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

// DraftResponse is the result from /api/v1/draft.
type DraftResponse struct {
	Draft         string   `json:"draft"`
	PlanExcerpt   string   `json:"plan_excerpt,omitempty"`
	ContextUsed   []string `json:"context_used"`
	TokensInPrompt int     `json:"tokens_in_prompt"`
	TokensInDraft  int     `json:"tokens_in_draft"`
	ModelUsed      string  `json:"model_used"`
}

// Draft calls the API to generate a speculative code draft using Ollama.
// Returns an error if CIAM_CODE_MODEL is not set on the server.
func (c *Client) Draft(req DraftRequest) (*DraftResponse, error) {
	var result DraftResponse
	err := c.post("/api/v1/draft", req, &result)
	return &result, err
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
