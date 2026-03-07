package api

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	djangoIndexer "github.com/smkbarbosa/context-ia-manager/internal/indexer/django"
	"github.com/smkbarbosa/context-ia-manager/internal/cache"
	"github.com/smkbarbosa/context-ia-manager/internal/config"
	"github.com/smkbarbosa/context-ia-manager/internal/embeddings"
	"github.com/smkbarbosa/context-ia-manager/internal/indexer"
	"github.com/smkbarbosa/context-ia-manager/internal/memory"
	"github.com/smkbarbosa/context-ia-manager/internal/search"
	"github.com/smkbarbosa/context-ia-manager/internal/storage"
)

//go:embed status.html
var statusHTMLTmpl string

// Server is the ciam REST API.
type Server struct {
	cfg         *config.Config
	db          *storage.DB
	embedder    *embeddings.Client
	memory      *memory.Service
	searchCache *cache.SearchCache
}

// NewServer initialises the server and its dependencies.
func NewServer(cfg *config.Config) (*Server, error) {
	db, err := storage.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}

	emb := embeddings.New(cfg.OllamaURL, cfg.OllamaModel)
	mem := memory.New(db, emb)

	// L2 cache usa a mesma pasta do banco principal, arquivo separado.
	cacheDBPath := cfg.DBPath[:len(cfg.DBPath)-len(".db")] + "-search-cache.db"
	sc, err := cache.NewSearchCache(500, 30*time.Minute, cacheDBPath)
	if err != nil {
		log.Printf("[cache] falha ao inicializar L2 (%v); usando apenas L1", err)
		sc, _ = cache.NewSearchCache(500, 30*time.Minute, "")
	}

	return &Server{
		cfg:         cfg,
		db:          db,
		embedder:    emb,
		memory:      mem,
		searchCache: sc,
	}, nil
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe(addr string) error {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /api/v1/project/index", s.handleIndex)
	mux.HandleFunc("POST /api/v1/project/chunks", s.handleChunks)
	mux.HandleFunc("POST /api/v1/project/file", s.handleFileIndex)
	mux.HandleFunc("POST /api/v1/search", s.handleSearch)
	mux.HandleFunc("POST /api/v1/memory/store", s.handleRemember)
	mux.HandleFunc("POST /api/v1/memory/recall", s.handleRecall)
	mux.HandleFunc("POST /api/v1/context/compress", s.handleCompress)
	mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/project/map", s.handleDjangoMap)
	mux.HandleFunc("POST /api/v1/metrics/tool", s.handleRecordTool)
	mux.HandleFunc("GET /status", s.handleStatusPage)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/status", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})

	fmt.Printf("ciam API listening on %s\n", addr)
	return http.ListenAndServe(addr, mux)
}

// ---- Handlers ----

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectPath string `json:"project_path"`
		ProjectID   string `json:"project_id"`
		ProjectType string `json:"project_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ProjectID == "" {
		req.ProjectID = filepath.Base(req.ProjectPath)
	}

	if req.ProjectType == "" {
		req.ProjectType = indexer.DetectType(req.ProjectPath)
	}

	var chunks []storage.Chunk
	var err error

	if req.ProjectType == "django" {
		idx := djangoIndexer.New(req.ProjectPath, req.ProjectID)
		chunks, err = idx.Index()
	} else {
		idx := indexer.New(req.ProjectPath, req.ProjectID)
		chunks, err = idx.Index()
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Generate embeddings for all chunks
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}
	vectors, err := s.embedder.EmbedBatch(texts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "embedding failed: "+err.Error())
		return
	}
	for i := range chunks {
		chunks[i].Embedding = vectors[i]
	}

	if err := s.db.UpsertChunks(req.ProjectID, chunks); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project_id":      req.ProjectID,
		"project_type":    req.ProjectType,
		"chunks_indexed":  len(chunks),
		"files_processed": countFiles(chunks),
	})
}

// handleChunks receives pre-chunked text from the CLI (which ran on the host),
// generates embeddings via Ollama and stores everything in SQLite.
// This is the primary indexing path when the API runs inside Docker.
// Set reset=true on the first batch of a full re-index to clear stale data;
// subsequent batches should send reset=false so they append rather than wipe.
func (s *Server) handleChunks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID   string `json:"project_id"`
		ProjectType string `json:"project_type"`
		Reset       bool   `json:"reset"`
		Chunks      []struct {
			FilePath  string `json:"file_path"`
			ChunkType string `json:"chunk_type"`
			Content   string `json:"content"`
		} `json:"chunks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Chunks) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"project_id":     req.ProjectID,
			"chunks_indexed": 0,
			"files_processed": 0,
		})
		return
	}

	// Clear existing chunks when this is the first batch of a full re-index.
	if req.Reset {
		if err := s.db.ClearProjectChunks(req.ProjectID); err != nil {
			writeError(w, http.StatusInternalServerError, "clear failed: "+err.Error())
			return
		}
		log.Printf("[index] project=%s cleared for full re-index", req.ProjectID)
	}

	const batchSize = 20
	total := len(req.Chunks)
	log.Printf("[index] project=%s total_chunks=%d batch_size=%d reset=%v", req.ProjectID, total, batchSize, req.Reset)

	var indexed int
	for start := 0; start < total; start += batchSize {
		end := start + batchSize
		if end > total {
			end = total
		}
		window := req.Chunks[start:end]

		texts := make([]string, len(window))
		chunks := make([]storage.Chunk, len(window))
		for i, c := range window {
			texts[i] = c.Content
			chunks[i] = storage.Chunk{
				ProjectID: req.ProjectID,
				FilePath:  c.FilePath,
				ChunkType: c.ChunkType,
				Content:   c.Content,
			}
		}

		vectors, err := s.embedder.EmbedBatch(texts)
		if err != nil {
			// Fallback: embed um a um, pulando chunks que excedem o contexto do modelo.
			log.Printf("[index] batch embed falhou (%v); modo fallback chunk-a-chunk", err)
			var goodChunks []storage.Chunk
			for i, t := range texts {
				vec, serr := s.embedder.Embed(t)
				if serr != nil {
					log.Printf("[index] ignorando chunk %d (%s): %v", start+i+1, chunks[i].FilePath, serr)
					continue
				}
				c := chunks[i]
				c.Embedding = vec
				goodChunks = append(goodChunks, c)
			}
			if len(goodChunks) > 0 {
				if err := s.db.AppendChunks(req.ProjectID, goodChunks); err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				indexed += len(goodChunks)
			}
			log.Printf("[index] progresso %d/%d chunks armazenados (fallback)", indexed, total)
			continue
		}
		for i := range chunks {
			chunks[i].Embedding = vectors[i]
		}
		if err := s.db.AppendChunks(req.ProjectID, chunks); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		indexed += len(chunks)
		log.Printf("[index] progress %d/%d chunks stored", indexed, total)
	}

	// Invalida cache de busca do projeto — novo índice pode mudar resultados.
	s.searchCache.InvalidateProject(req.ProjectID)

	// Count unique files from the original request.
	seen := make(map[string]struct{}, total)
	for _, c := range req.Chunks {
		seen[c.FilePath] = struct{}{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project_id":      req.ProjectID,
		"project_type":    req.ProjectType,
		"chunks_indexed":  indexed,
		"files_processed": len(seen),
	})
}

// handleFileIndex indexes a single file incrementally.
// The client sends a pre-computed SHA-256 hash; if it matches what is stored,
// the request is acknowledged without re-embedding (saving GPU time).
func (s *Server) handleFileIndex(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID string `json:"project_id"`
		FileHash  string `json:"file_hash"`
		Chunks    []struct {
			FilePath  string `json:"file_path"`
			ChunkType string `json:"chunk_type"`
			Content   string `json:"content"`
		} `json:"chunks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Chunks) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"updated": false, "chunks_indexed": 0})
		return
	}

	filePath := req.Chunks[0].FilePath

	// Check hash — skip embedding if content unchanged.
	if req.FileHash != "" && s.db.GetFileHash(req.ProjectID, filePath) == req.FileHash {
		writeJSON(w, http.StatusOK, map[string]any{
			"project_id":    req.ProjectID,
			"file_path":     filePath,
			"updated":       false,
			"chunks_indexed": 0,
		})
		return
	}

	texts := make([]string, len(req.Chunks))
	chunks := make([]storage.Chunk, len(req.Chunks))
	for i, c := range req.Chunks {
		texts[i] = c.Content
		chunks[i] = storage.Chunk{
			ProjectID: req.ProjectID,
			FilePath:  c.FilePath,
			ChunkType: c.ChunkType,
			Content:   c.Content,
		}
	}

	vectors, err := s.embedder.EmbedBatch(texts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "embedding failed: "+err.Error())
		return
	}
	for i := range chunks {
		chunks[i].Embedding = vectors[i]
	}

	updated, err := s.db.UpsertFileChunks(req.ProjectID, filePath, req.FileHash, chunks)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Invalidate search cache for this project.
	s.searchCache.InvalidateProject(req.ProjectID)

	writeJSON(w, http.StatusOK, map[string]any{
		"project_id":     req.ProjectID,
		"file_path":      filePath,
		"updated":        updated,
		"chunks_indexed": len(chunks),
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query     string `json:"query"`
		ProjectID string `json:"project_id"`
		ChunkType string `json:"chunk_type"`
		Limit     int    `json:"limit"`
		Compress  bool   `json:"compress"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Limit == 0 {
		req.Limit = 5
	}

	type chunkOut struct {
		FilePath  string  `json:"file_path"`
		ChunkType string  `json:"chunk_type"`
		Content   string  `json:"content"`
		Score     float32 `json:"score"`
	}

	// L1/L2: verifica cache de busca antes de chamar Ollama + SQLite.
	cacheKey := cache.SearchKey(req.ProjectID, req.Query, req.ChunkType, req.Limit)
	if cached, ok := s.searchCache.Get(cacheKey); ok {
		out := make([]chunkOut, len(cached))
		for i, c := range cached {
			content := c.Content
			if req.Compress {
				content = search.Compress(content)
			}
			out[i] = chunkOut{FilePath: c.FilePath, ChunkType: c.ChunkType, Content: content, Score: c.Score}
		}
		writeJSON(w, http.StatusOK, map[string]any{"chunks": out, "cached": true})
		return
	}

	vec, err := s.embedder.Embed(req.Query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "embedding failed: "+err.Error())
		return
	}

	raw, err := s.db.SearchChunks(req.ProjectID, vec, req.ChunkType, req.Limit*3)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	results := search.Hybrid(req.Query, raw)
	if len(results) > req.Limit {
		results = results[:req.Limit]
	}

	// Track tokens served: approx 1 token per 4 chars of content returned.
	var tokensServed int64
	for _, r := range results {
		tokensServed += int64(len(r.Content)) / 4
	}
	if tokensServed > 0 {
		s.db.IncrMetric("tokens_served_via_search", tokensServed)
	}

	// Armazena no cache para próximas chamadas idênticas.
	cacheable := make([]cache.SearchResult, len(results))
	for i, r := range results {
		cacheable[i] = cache.SearchResult{
			FilePath: r.FilePath, ChunkType: r.ChunkType,
			Content: r.Content, Score: r.RRFScore,
		}
	}
	s.searchCache.Set(cacheKey, cacheable)

	out := make([]chunkOut, len(results))
	for i, r := range results {
		content := r.Content
		if req.Compress {
			content = search.Compress(content)
		}
		out[i] = chunkOut{
			FilePath:  r.FilePath,
			ChunkType: r.ChunkType,
			Content:   content,
			Score:     r.RRFScore,
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"chunks": out})
}

func (s *Server) handleRemember(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
		Type    string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Type == "" {
		req.Type = "note"
	}
	if err := s.memory.Remember(req.Type, req.Content); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stored"})
}

func (s *Server) handleRecall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Limit == 0 {
		req.Limit = 5
	}
	memories, err := s.memory.Recall(req.Query, req.Limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memories": memories})
}

func (s *Server) handleCompress(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	compressed := search.Compress(req.Content)
	reduction := 0
	if len(req.Content) > 0 {
		reduction = 100 - (len(compressed)*100)/len(req.Content)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"compressed":       compressed,
		"reduction_pct":    reduction,
		"original_len":     len(req.Content),
		"compressed_len":   len(compressed),
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	metrics, err := s.db.Metrics()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Agrega hits dos dois caches.
	metrics.CacheHits = int(s.embedder.EmbedCacheHits()) + int(s.searchCache.Hits())
	writeJSON(w, http.StatusOK, metrics)
}

func (s *Server) handleDjangoMap(w http.ResponseWriter, r *http.Request) {
	projectPath := r.URL.Query().Get("path")
	if projectPath == "" {
		projectPath = s.cfg.ProjectPath
	}
	projectID := filepath.Base(projectPath)

	idx := djangoIndexer.New(projectPath, projectID)
	appMap, err := idx.AppMap()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, appMap)
}

// handleRecordTool persists a single MCP tool invocation from the MCP server process.
func (s *Server) handleRecordTool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tool      string `json:"tool"`
		Query     string `json:"query"`
		LatencyMs int64  `json:"latency_ms"`
		IsError   bool   `json:"is_error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.db.RecordToolCall(req.Tool, req.Query, req.LatencyMs, req.IsError); err != nil {
		log.Printf("[metrics] RecordToolCall failed: %v", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleStatusPage serves GET /status — HTML if the client accepts it, JSON otherwise.
func (s *Server) handleStatusPage(w http.ResponseWriter, r *http.Request) {
	metrics, err := s.db.Metrics()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	metrics.CacheHits = int(s.embedder.EmbedCacheHits()) + int(s.searchCache.Hits())

	totalMCPCalls := int64(0)
	for _, t := range metrics.MCPStats {
		totalMCPCalls += t.CallCount
	}

	// Negotiate: HTML or JSON.
	acceptHTML := strings.Contains(r.Header.Get("Accept"), "text/html")
	if !acceptHTML {
		writeJSON(w, http.StatusOK, map[string]any{
			"metrics":         metrics,
			"total_mcp_calls": totalMCPCalls,
		})
		return
	}

	// Render HTML.
	funcMap := template.FuncMap{
		// pct returns integer percentage of part relative to total (0-100).
		"pct": func(part, total int64) int64 {
			if total == 0 {
				return 0
			}
			v := part * 100 / total
			if v > 100 {
				return 100
			}
			return v
		},
		// slice returns s[start:end], safe against out-of-range.
		"slice": func(s string, start, end int) string {
			if start < 0 {
				start = 0
			}
			if end > len(s) {
				end = len(s)
			}
			if start >= end {
				return s
			}
			return s[start:end]
		},
		// mul is needed for intermediate calculations inside templates.
		"mul": func(a int64, b int) int64 { return a * int64(b) },
	}
	tmpl, err := template.New("status").Funcs(funcMap).Parse(statusHTMLTmpl)
	if err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, map[string]any{ //nolint:errcheck
		"Metrics":       metrics,
		"TotalMCPCalls": totalMCPCalls,
		"Now":           time.Now().Format("02/01/2006 15:04:05"),
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func countFiles(chunks []storage.Chunk) int {
	seen := map[string]bool{}
	for _, c := range chunks {
		seen[c.FilePath] = true
	}
	return len(seen)
}

// normaliseURL ensures the base URL has no trailing slash.
func normaliseURL(u string) string {
	return strings.TrimRight(u, "/")
}
