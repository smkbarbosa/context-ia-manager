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

// StatusMetrics are aggregated stats returned by /status.
type StatusMetrics struct {
	ProjectsIndexed      int
	TotalChunks          int
	MemoriesStored       int
	CacheHits            int
	EstimatedTokensSaved int
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
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_chunks_project ON chunks(project_id);
		CREATE INDEX IF NOT EXISTS idx_chunks_type    ON chunks(project_id, chunk_type);

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
	`)
	return err
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

	// rough estimate: average ~200 tokens per chunk, stored once, reused many times
	m.EstimatedTokensSaved = m.TotalChunks * 180

	return m, nil
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
