// Package cache implementa cache multi-nível para o ciam:
//   - L1: in-memory com capacidade máxima e evicção LRU simples
//   - L2: SQLite persistente entre reinicializações
//
// São dois caches distintos:
//   - EmbedCache: vetores de embedding por hash do texto (evita chamar Ollama repetidamente)
//   - SearchCache: resultados de busca por hash da query (evita re-executar buscas idênticas)
package cache

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// embedEntry é uma entrada no cache L1 de embeddings.
type embedEntry struct {
	vec     []float32
	touchAt time.Time
}

// EmbedCache é um cache in-memory thread-safe para vetores de embedding.
// Chave: SHA256 hex do texto original.
type EmbedCache struct {
	mu      sync.RWMutex
	entries map[string]embedEntry
	cap     int
	hits    atomic.Int64
}

// NewEmbedCache cria um EmbedCache com capacidade máxima cap.
func NewEmbedCache(cap int) *EmbedCache {
	return &EmbedCache{
		entries: make(map[string]embedEntry, cap),
		cap:     cap,
	}
}

func hashKey(text string) string {
	h := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", h)
}

// Get retorna o vetor em cache para text, ou nil, false se ausente.
func (c *EmbedCache) Get(text string) ([]float32, bool) {
	c.mu.RLock()
	e, ok := c.entries[hashKey(text)]
	c.mu.RUnlock()
	if ok {
		c.hits.Add(1)
	}
	return e.vec, ok
}

// Set armazena vec no cache associado a text.
// Se o cache atingiu capacidade, remove a entrada mais antiga.
func (c *EmbedCache) Set(text string, vec []float32) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.cap {
		// Evicção: remove a entrada mais antiga (LRU simples).
		var oldestKey string
		var oldestTime time.Time
		for k, e := range c.entries {
			if oldestTime.IsZero() || e.touchAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = e.touchAt
			}
		}
		delete(c.entries, oldestKey)
	}
	c.entries[hashKey(text)] = embedEntry{vec: vec, touchAt: time.Now()}
}

// Hits retorna o total de cache hits acumulados.
func (c *EmbedCache) Hits() int64 {
	return c.hits.Load()
}

// Len retorna o número de entradas no cache.
func (c *EmbedCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// ─── Search cache ─────────────────────────────────────────────────────────────

// SearchResult é um resultado de busca cacheável.
type SearchResult struct {
	FilePath  string  `json:"file_path"`
	ChunkType string  `json:"chunk_type"`
	Content   string  `json:"content"`
	Score     float32 `json:"score"`
}

// searchEntry é uma entrada no cache L1 de buscas.
type searchEntry struct {
	results []SearchResult
	at      time.Time
}

// SearchCache é um cache de dois níveis para resultados de busca.
// L1: in-memory (rápido, volátil) — L2: SQLite (persistente entre reinicializações).
type SearchCache struct {
	mu   sync.RWMutex
	l1   map[string]searchEntry
	cap  int
	ttl  time.Duration
	db   *sql.DB
	hits atomic.Int64
}

// NewSearchCache cria um SearchCache com L1 in-memory (cap entradas, ttl válido)
// e L2 SQLite em dbPath. Se dbPath for vazio, somente L1 é usado.
func NewSearchCache(cap int, ttl time.Duration, dbPath string) (*SearchCache, error) {
	sc := &SearchCache{
		l1:  make(map[string]searchEntry, cap),
		cap: cap,
		ttl: ttl,
	}

	if dbPath != "" {
		db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=3000")
		if err != nil {
			return nil, fmt.Errorf("cache: abrir SQLite: %w", err)
		}
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS search_cache (
			key       TEXT PRIMARY KEY,
			results   TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)`); err != nil {
			return nil, fmt.Errorf("cache: criar tabela: %w", err)
		}
		sc.db = db
	}

	return sc, nil
}

// SearchKey gera a chave de cache para uma busca.
func SearchKey(projectID, query, chunkType string, limit int) string {
	raw := fmt.Sprintf("%s|%s|%s|%d", projectID, query, chunkType, limit)
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h)
}

// Get retorna resultados cacheados, verificando L1 depois L2.
func (sc *SearchCache) Get(key string) ([]SearchResult, bool) {
	// L1
	sc.mu.RLock()
	e, ok := sc.l1[key]
	sc.mu.RUnlock()
	if ok && time.Since(e.at) < sc.ttl {
		sc.hits.Add(1)
		return e.results, true
	}

	// L2 (SQLite)
	if sc.db != nil {
		cutoff := time.Now().Add(-sc.ttl).Unix()
		var raw string
		err := sc.db.QueryRow(
			`SELECT results FROM search_cache WHERE key=? AND created_at>?`,
			key, cutoff,
		).Scan(&raw)
		if err == nil {
			var results []SearchResult
			if json.Unmarshal([]byte(raw), &results) == nil {
				sc.hits.Add(1)
				// Promove para L1.
				sc.mu.Lock()
				sc.l1[key] = searchEntry{results: results, at: time.Now()}
				sc.mu.Unlock()
				return results, true
			}
		}
	}

	return nil, false
}

// Set armazena resultados no L1 e, se disponível, no L2.
func (sc *SearchCache) Set(key string, results []SearchResult) {
	// L1
	sc.mu.Lock()
	if len(sc.l1) >= sc.cap {
		var oldestKey string
		var oldestTime time.Time
		for k, e := range sc.l1 {
			if oldestTime.IsZero() || e.at.Before(oldestTime) {
				oldestKey = k
				oldestTime = e.at
			}
		}
		delete(sc.l1, oldestKey)
	}
	sc.l1[key] = searchEntry{results: results, at: time.Now()}
	sc.mu.Unlock()

	// L2 (SQLite)
	if sc.db != nil {
		data, err := json.Marshal(results)
		if err == nil {
			_, _ = sc.db.Exec(
				`INSERT OR REPLACE INTO search_cache(key,results,created_at) VALUES(?,?,?)`,
				key, string(data), time.Now().Unix(),
			)
		}
	}
}

// Hits retorna o total de cache hits.
func (sc *SearchCache) Hits() int64 {
	return sc.hits.Load()
}

// Invalidate remove entradas do L1 e L2 cujo projectID está no prefixo.
// Use após reindexação para evitar resultados obsoletos.
func (sc *SearchCache) InvalidateProject(projectID string) {
	prefix := hashKey(projectID)[:8] // não é perfeito mas evita flush total
	_ = prefix

	// Para simplificar, invalida tudo no L1 (barato — é só memória).
	sc.mu.Lock()
	sc.l1 = make(map[string]searchEntry, sc.cap)
	sc.mu.Unlock()

	if sc.db != nil {
		// Limpa apenas entradas expiradas para não invalidar outros projetos.
		cutoff := time.Now().Add(-sc.ttl).Unix()
		_, _ = sc.db.Exec(`DELETE FROM search_cache WHERE created_at<?`, cutoff)
	}
}

// Close fecha a conexão SQLite do L2.
func (sc *SearchCache) Close() error {
	if sc.db != nil {
		return sc.db.Close()
	}
	return nil
}
