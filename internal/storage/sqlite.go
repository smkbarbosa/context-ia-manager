// Package storage handles all SQLite persistence for ciam.
// Chunks are stored with their embeddings serialised as blobs.
// Cosine similarity search is done in Go until sqlite-vec is wired in.
package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Chunk is a piece of source code with its embedding and metadata.
type Chunk struct {
	ID          int64
	ProjectID   string
	FilePath    string
	ChunkType   string   // model, view, url, serializer, generic …
	Content     string
	Embedding   []float32
	Score       float32  // populated during search
	CreatedAt   time.Time
}

// Memory is a persistent note stored across sessions.
type Memory struct {
	ID        int64
	Type      string
	Content   string
	Embedding []float32
	CreatedAt string
}

// ProjectMetric holds per-project aggregated stats.
type ProjectMetric struct {
	ProjectID      string         `json:"project_id"`
	TotalChunks    int            `json:"total_chunks"`
	TotalFiles     int            `json:"total_files"`
	TokenEstimate  int64          `json:"token_estimate"`
	LastIndexed    string         `json:"last_indexed"`
	ChunkTypes     map[string]int `json:"chunk_types"`
	Searches       int64          `json:"searches,omitempty"`
}

// StatusMetrics are aggregated stats returned by /status.
type StatusMetrics struct {
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

// DB wraps a SQLite connection with ciam-specific helpers.
type DB struct {
	conn *sql.DB
}

// Open opens (or creates) the SQLite database at dbPath.
func Open(dbPath string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}

	conn, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}

	db := &DB{conn: conn}
	return db, db.migrate()
}

func (db *DB) migrate() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS chunks (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id  TEXT NOT NULL,
			file_path   TEXT NOT NULL,
			chunk_type  TEXT NOT NULL DEFAULT 'generic',
			content     TEXT NOT NULL,
			embedding   BLOB,
			file_hash   TEXT NOT NULL DEFAULT '',
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_chunks_project ON chunks(project_id);
		CREATE INDEX IF NOT EXISTS idx_chunks_type    ON chunks(project_id, chunk_type);
		CREATE INDEX IF NOT EXISTS idx_chunks_file    ON chunks(project_id, file_path);

		CREATE TABLE IF NOT EXISTS memories (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			type        TEXT NOT NULL DEFAULT 'note',
			content     TEXT NOT NULL,
			embedding   BLOB,
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS metrics (
			key   TEXT PRIMARY KEY,
			value INTEGER NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS mcp_tool_calls (
			tool_name        TEXT PRIMARY KEY,
			call_count       INTEGER NOT NULL DEFAULT 0,
			error_count      INTEGER NOT NULL DEFAULT 0,
			total_latency_ms INTEGER NOT NULL DEFAULT 0,
			last_called_at   DATETIME
		);

		CREATE TABLE IF NOT EXISTS mcp_tool_queries (
			tool_name  TEXT NOT NULL,
			query      TEXT NOT NULL,
			call_count INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (tool_name, query)
		);
	`)
	if err != nil {
		return err
	}

	// Incremental migrations — silently ignored if column/index already exists.
	db.conn.Exec(`ALTER TABLE chunks ADD COLUMN file_hash TEXT NOT NULL DEFAULT ''`) //nolint:errcheck

	// Ensure token-tracking key exists in metrics table.
	db.conn.Exec(`INSERT OR IGNORE INTO metrics (key, value) VALUES ('tokens_served_via_search', 0)`) //nolint:errcheck

	return nil
}

// MCPToolStat holds metrics for a single MCP tool.
type MCPToolStat struct {
	ToolName     string   `json:"tool_name"`
	CallCount    int64    `json:"call_count"`
	ErrorCount   int64    `json:"error_count"`
	AvgLatencyMs float64  `json:"avg_latency_ms"`
	LastCalledAt string   `json:"last_called_at"`
	TopQueries   []string `json:"top_queries,omitempty"`
}

// RecordToolCall upserts a single MCP tool invocation into mcp_tool_calls.
func (db *DB) RecordToolCall(toolName, query string, latencyMs int64, isError bool) error {
	errVal := int64(0)
	if isError {
		errVal = 1
	}
	_, err := db.conn.Exec(`
		INSERT INTO mcp_tool_calls (tool_name, call_count, error_count, total_latency_ms, last_called_at)
		VALUES (?, 1, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(tool_name) DO UPDATE SET
			call_count       = call_count + 1,
			error_count      = error_count + ?,
			total_latency_ms = total_latency_ms + ?,
			last_called_at   = CURRENT_TIMESTAMP
	`, toolName, errVal, latencyMs, errVal, latencyMs)
	if err != nil {
		return err
	}
	if query == "" {
		return nil
	}
	if len(query) > 200 {
		query = query[:200]
	}
	_, err = db.conn.Exec(`
		INSERT INTO mcp_tool_queries (tool_name, query, call_count)
		VALUES (?, ?, 1)
		ON CONFLICT(tool_name, query) DO UPDATE SET call_count = call_count + 1
	`, toolName, query)
	return err
}

// MCPStats returns per-tool metrics sorted by call count descending.
func (db *DB) MCPStats() ([]MCPToolStat, error) {
	rows, err := db.conn.Query(`
		SELECT tool_name, call_count, error_count, total_latency_ms,
		       COALESCE(last_called_at, '')
		FROM mcp_tool_calls
		ORDER BY call_count DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []MCPToolStat
	for rows.Next() {
		var s MCPToolStat
		var totalLatency int64
		if err := rows.Scan(&s.ToolName, &s.CallCount, &s.ErrorCount, &totalLatency, &s.LastCalledAt); err != nil {
			return nil, err
		}
		if s.CallCount > 0 {
			s.AvgLatencyMs = float64(totalLatency) / float64(s.CallCount)
		}
		// Top 5 queries for this tool.
		qRows, err := db.conn.Query(`
			SELECT query FROM mcp_tool_queries
			WHERE tool_name = ?
			ORDER BY call_count DESC LIMIT 5
		`, s.ToolName)
		if err == nil {
			for qRows.Next() {
				var q string
				if qRows.Scan(&q) == nil {
					s.TopQueries = append(s.TopQueries, q)
				}
			}
			qRows.Close()
		}
		stats = append(stats, s)
	}
	return stats, nil
}

// Close closes the underlying connection.
func (db *DB) Close() error { return db.conn.Close() }

// UpsertChunks deletes existing chunks for the project+file and inserts the new ones.
func (db *DB) UpsertChunks(projectID string, chunks []Chunk) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// Remove old chunks for this project (full re-index)
	if _, err := tx.Exec(`DELETE FROM chunks WHERE project_id = ?`, projectID); err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO chunks (project_id, file_path, chunk_type, content, embedding)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, c := range chunks {
		blob, err := json.Marshal(c.Embedding)
		if err != nil {
			return err
		}
		if _, err := stmt.Exec(projectID, c.FilePath, c.ChunkType, c.Content, blob); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetFileHash returns the stored content hash for a specific file,
// or "" if the file has not been indexed yet.
func (db *DB) GetFileHash(projectID, filePath string) string {
	var hash string
	db.conn.QueryRow( //nolint:errcheck
		`SELECT COALESCE(file_hash, '') FROM chunks WHERE project_id = ? AND file_path = ? LIMIT 1`,
		projectID, filePath,
	).Scan(&hash)
	return hash
}

// UpsertFileChunks replaces chunks for a single file atomically.
// Returns (false, nil) when the file hash is unchanged — nothing to do.
func (db *DB) UpsertFileChunks(projectID, filePath, newHash string, chunks []Chunk) (bool, error) {
	if newHash != "" && db.GetFileHash(projectID, filePath) == newHash {
		return false, nil // content unchanged, skip embedding
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(
		`DELETE FROM chunks WHERE project_id = ? AND file_path = ?`, projectID, filePath,
	); err != nil {
		return false, err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO chunks (project_id, file_path, chunk_type, content, embedding, file_hash)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return false, err
	}
	defer stmt.Close()

	for _, c := range chunks {
		blob, err := json.Marshal(c.Embedding)
		if err != nil {
			return false, err
		}
		if _, err := stmt.Exec(projectID, c.FilePath, c.ChunkType, c.Content, blob, newHash); err != nil {
			return false, err
		}
	}

	return true, tx.Commit()
}

// IncrMetric atomically increments a named counter in the metrics table.
func (db *DB) IncrMetric(key string, delta int64) {
	db.conn.Exec( //nolint:errcheck
		`INSERT INTO metrics (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = value + ?`,
		key, delta, delta,
	)
}

// SearchChunks returns the top-k chunks ranked by cosine similarity to queryVec.
func (db *DB) SearchChunks(projectID string, queryVec []float32, chunkType string, limit int) ([]Chunk, error) {
	q := `SELECT id, file_path, chunk_type, content, embedding FROM chunks WHERE project_id = ?`
	qargs := []any{projectID}

	if chunkType != "" {
		q += ` AND chunk_type = ?`
		qargs = append(qargs, chunkType)
	}

	rows, err := db.conn.Query(q, qargs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []Chunk
	for rows.Next() {
		var c Chunk
		var blob []byte
		if err := rows.Scan(&c.ID, &c.FilePath, &c.ChunkType, &c.Content, &blob); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(blob, &c.Embedding); err != nil {
			continue
		}
		c.Score = cosine(queryVec, c.Embedding)
		candidates = append(candidates, c)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

// StoreMemory persists a memory entry.
func (db *DB) StoreMemory(memType, content string, embedding []float32) error {
	blob, err := json.Marshal(embedding)
	if err != nil {
		return err
	}
	_, err = db.conn.Exec(
		`INSERT INTO memories (type, content, embedding) VALUES (?, ?, ?)`,
		memType, content, blob,
	)
	return err
}

// RecallMemories returns the top-k memories ranked by similarity.
func (db *DB) RecallMemories(queryVec []float32, limit int) ([]Memory, error) {
	rows, err := db.conn.Query(`SELECT id, type, content, embedding, created_at FROM memories`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scored struct {
		mem   Memory
		score float32
	}
	var candidates []scored

	for rows.Next() {
		var m Memory
		var blob []byte
		if err := rows.Scan(&m.ID, &m.Type, &m.Content, &blob, &m.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(blob, &m.Embedding); err != nil {
			continue
		}
		candidates = append(candidates, scored{mem: m, score: cosine(queryVec, m.Embedding)})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	result := make([]Memory, 0, limit)
	for i, c := range candidates {
		if i >= limit {
			break
		}
		result = append(result, c.mem)
	}
	return result, nil
}

// Metrics returns aggregated stats.
func (db *DB) Metrics() (*StatusMetrics, error) {
	m := &StatusMetrics{}

	row := db.conn.QueryRow(`SELECT COUNT(DISTINCT project_id), COUNT(*) FROM chunks`)
	if err := row.Scan(&m.ProjectsIndexed, &m.TotalChunks); err != nil {
		return nil, fmt.Errorf("chunk metrics: %w", err)
	}

	row = db.conn.QueryRow(`SELECT COUNT(*) FROM memories`)
	if err := row.Scan(&m.MemoriesStored); err != nil {
		return nil, fmt.Errorf("memory metrics: %w", err)
	}

	// Approximate project size in tokens (avg chunk ≈ 400 chars ≈ 100 tokens).
	row = db.conn.QueryRow(`SELECT COALESCE(SUM(LENGTH(content)), 0) FROM chunks`)
	var totalChars int64
	row.Scan(&totalChars) //nolint:errcheck
	m.TotalProjectTokens = totalChars / 4

	// Tokens actually served to the AI via search results.
	db.conn.QueryRow( //nolint:errcheck
		`SELECT COALESCE(value, 0) FROM metrics WHERE key = 'tokens_served_via_search'`,
	).Scan(&m.TokensServedViaSearch)

	// Estimated savings = full project - what was actually served.
	if m.TotalProjectTokens > m.TokensServedViaSearch {
		m.EstimatedTokensSaved = int(m.TotalProjectTokens - m.TokensServedViaSearch)
	}

	mcpStats, err := db.MCPStats()
	if err == nil {
		m.MCPStats = mcpStats
	}

	projects, err := db.ProjectMetrics()
	if err == nil {
		m.Projects = projects
	}

	return m, nil
}

// ProjectMetrics returns per-project stats sorted by last indexed desc.
func (db *DB) ProjectMetrics() ([]ProjectMetric, error) {
	// Aggregate totals per project.
	rows, err := db.conn.Query(`
		SELECT
			project_id,
			COUNT(*)                        AS total_chunks,
			COUNT(DISTINCT file_path)       AS total_files,
			COALESCE(SUM(LENGTH(content)),0) / 4 AS token_estimate,
			COALESCE(MAX(created_at), '')   AS last_indexed
		FROM chunks
		GROUP BY project_id
		ORDER BY last_indexed DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []ProjectMetric
	for rows.Next() {
		var p ProjectMetric
		if err := rows.Scan(&p.ProjectID, &p.TotalChunks, &p.TotalFiles, &p.TokenEstimate, &p.LastIndexed); err != nil {
			return nil, err
		}
		p.ChunkTypes = map[string]int{}
		projects = append(projects, p)
	}
	rows.Close() //nolint:errcheck

	// Fetch chunk_type breakdown per project separately (simpler loop).
	for i, p := range projects {
		tRows, err := db.conn.Query(`
			SELECT chunk_type, COUNT(*) FROM chunks
			WHERE project_id = ?
			GROUP BY chunk_type
			ORDER BY chunk_type
		`, p.ProjectID)
		if err != nil {
			continue
		}
		for tRows.Next() {
			var ct string
			var cnt int
			if tRows.Scan(&ct, &cnt) == nil {
				projects[i].ChunkTypes[ct] = cnt
			}
		}
		tRows.Close() //nolint:errcheck
	}

	return projects, nil
}

// cosine computes cosine similarity between two vectors.
func cosine(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return float32(dot / denom)
}
