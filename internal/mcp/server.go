// Package mcp implements the MCP stdio server that exposes ciam tools
// to VSCode, Antigravity, and any other MCP-compatible client.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
"github.com/mark3labs/mcp-go/server"
djangoIndexer "github.com/smkbarbosa/context-ia-manager/internal/indexer/django"
"github.com/smkbarbosa/context-ia-manager/internal/api"
"github.com/smkbarbosa/context-ia-manager/internal/config"
"github.com/smkbarbosa/context-ia-manager/internal/search"
)

// Server wraps the MCP stdio server.
type Server struct {
	cfg    *config.Config
	client *api.Client
}

// NewServer creates a new MCP server.
func NewServer(cfg *config.Config) *Server {
	return &Server{
		cfg:    cfg,
		client: api.NewClient(cfg.APIURL),
	}
}

// Serve starts the MCP stdio server and blocks until stdin closes.
func (s *Server) Serve() error {
	srv := server.NewMCPServer(
"ciam",
"1.0.0",
server.WithToolCapabilities(true),
)

	s.registerTools(srv)

	return server.ServeStdio(srv)
}

// withMetrics wraps a tool handler to measure latency and record usage metrics.
// queryParam is the request field name used as the "query" label (empty = skip).
// Recording is fire-and-forget so it never delays the response.
func (s *Server) withMetrics(tool, queryParam string, fn func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error)) func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		start := time.Now()
		result, err := fn(ctx, req)
		latencyMs := time.Since(start).Milliseconds()
		isError := err != nil || (result != nil && result.IsError)
		query := ""
		if queryParam != "" {
			query = req.GetString(queryParam, "")
		}
		go s.client.RecordTool(tool, query, latencyMs, isError) //nolint:errcheck
		return result, err
	}
}

func (s *Server) registerTools(srv *server.MCPServer) {
	srv.AddTool(
		mcpgo.NewTool("ciam_index",
			mcpgo.WithDescription("Index a project directory for semantic search. "+
				"Call this once when you open a new project or after significant changes."),
			mcpgo.WithString("project_path",
				mcpgo.Required(),
				mcpgo.Description("Absolute path to the project root")),
			mcpgo.WithString("project_type",
				mcpgo.Description("Project type: django, python, generic (auto-detected if omitted)")),
		),
		s.withMetrics("ciam_index", "project_path", s.handleIndex),
	)

	srv.AddTool(
		mcpgo.NewTool("ciam_search",
			mcpgo.WithDescription("Semantic + keyword search in the indexed project. "+
				"Returns relevant code chunks ranked by hybrid score. "+
				"Use this INSTEAD of reading full files to reduce context tokens."),
			mcpgo.WithString("query",
				mcpgo.Required(),
				mcpgo.Description("Natural language or code search query")),
			mcpgo.WithString("project_path",
				mcpgo.Description("Project path (defaults to workspace folder)")),
			mcpgo.WithString("chunk_type",
				mcpgo.Description("Filter: model, view, url, serializer, task, test, generic")),
			mcpgo.WithNumber("limit",
				mcpgo.Description("Max results (default 5)")),
			mcpgo.WithBoolean("compress",
				mcpgo.Description("Compress results to reduce tokens (default false)")),
		),
		s.withMetrics("ciam_search", "query", s.handleSearch),
	)

	srv.AddTool(
		mcpgo.NewTool("ciam_remember",
			mcpgo.WithDescription("Store an important decision, note, or architectural choice "+
				"in persistent memory. Available across sessions and projects."),
			mcpgo.WithString("content",
				mcpgo.Required(),
				mcpgo.Description("The information to remember")),
			mcpgo.WithString("type",
				mcpgo.Description("Memory type: decision, note, context, bug, architecture")),
		),
		s.withMetrics("ciam_remember", "content", s.handleRemember),
	)

	srv.AddTool(
		mcpgo.NewTool("ciam_recall",
			mcpgo.WithDescription("Search stored memories from previous sessions."),
			mcpgo.WithString("query",
				mcpgo.Required(),
				mcpgo.Description("What you want to recall")),
		),
		s.withMetrics("ciam_recall", "query", s.handleRecall),
	)

	srv.AddTool(
		mcpgo.NewTool("ciam_compress",
			mcpgo.WithDescription("Compress code: keeps signatures, removes docstrings/comments. "+
				"Reduces tokens by 70-90%."),
			mcpgo.WithString("content",
				mcpgo.Required(),
				mcpgo.Description("Code content to compress")),
		),
		s.withMetrics("ciam_compress", "", s.handleCompress),
	)

	srv.AddTool(
		mcpgo.NewTool("ciam_context",
			mcpgo.WithDescription("Search the project AND compress results in one call. "+
				"Maximum token efficiency — use this as your default discovery tool."),
			mcpgo.WithString("query",
				mcpgo.Required(),
				mcpgo.Description("What you are looking for")),
			mcpgo.WithString("project_path",
				mcpgo.Description("Project path (defaults to workspace folder)")),
			mcpgo.WithString("chunk_type",
				mcpgo.Description("Filter: model, view, url, serializer, task, test, generic")),
			mcpgo.WithNumber("limit",
				mcpgo.Description("Max results (default 5)")),
		),
		s.withMetrics("ciam_context", "query", s.handleContext),
	)

	srv.AddTool(
		mcpgo.NewTool("ciam_django_map",
			mcpgo.WithDescription("Returns a structural map of the Django project: "+
				"apps, models, views, urls, serializers. Great for onboarding."),
			mcpgo.WithString("project_path",
				mcpgo.Description("Absolute path to the Django project root")),
		),
		s.withMetrics("ciam_django_map", "", s.handleDjangoMap),
	)

	// ── Knowledge management tools (Fase 5) ─────────────────────────────────

	srv.AddTool(
		mcpgo.NewTool("ciam_adr_search",
			mcpgo.WithDescription("Search Architecture Decision Records (ADRs). "+
				"Use to understand WHY past architectural decisions were made before proposing changes."),
			mcpgo.WithString("query",
				mcpgo.Required(),
				mcpgo.Description("What you want to find in ADRs")),
			mcpgo.WithString("project_path",
				mcpgo.Description("Project path (defaults to workspace folder)")),
			mcpgo.WithNumber("limit",
				mcpgo.Description("Max results (default 5)")),
		),
		s.withMetrics("ciam_adr_search", "query", s.handleADRSearch),
	)

	srv.AddTool(
		mcpgo.NewTool("ciam_prd_search",
			mcpgo.WithDescription("Search Product Requirement Documents (PRDs). "+
				"Use to understand WHAT a feature must do and its acceptance criteria."),
			mcpgo.WithString("query",
				mcpgo.Required(),
				mcpgo.Description("What you want to find in PRDs")),
			mcpgo.WithString("project_path",
				mcpgo.Description("Project path (defaults to workspace folder)")),
			mcpgo.WithNumber("limit",
				mcpgo.Description("Max results (default 5)")),
		),
		s.withMetrics("ciam_prd_search", "query", s.handlePRDSearch),
	)

	srv.AddTool(
		mcpgo.NewTool("ciam_plan_search",
			mcpgo.WithDescription("Search implementation plans. "+
				"Use to understand the phased approach and success criteria for a feature."),
			mcpgo.WithString("query",
				mcpgo.Required(),
				mcpgo.Description("What you want to find in plans")),
			mcpgo.WithString("project_path",
				mcpgo.Description("Project path (defaults to workspace folder)")),
			mcpgo.WithNumber("limit",
				mcpgo.Description("Max results (default 5)")),
		),
		s.withMetrics("ciam_plan_search", "query", s.handlePlanSearch),
	)

	srv.AddTool(
		mcpgo.NewTool("ciam_research_search",
			mcpgo.WithDescription("Search research documents in docs/research/. "+
				"Use to find external knowledge that was ingested into this project."),
			mcpgo.WithString("query",
				mcpgo.Required(),
				mcpgo.Description("What you want to find in research docs")),
			mcpgo.WithString("project_path",
				mcpgo.Description("Project path (defaults to workspace folder)")),
			mcpgo.WithNumber("limit",
				mcpgo.Description("Max results (default 5)")),
		),
		s.withMetrics("ciam_research_search", "query", s.handleResearchSearch),
	)

	srv.AddTool(
		mcpgo.NewTool("ciam_decision_context",
			mcpgo.WithDescription("Get full decision context for a query in one call: "+
				"code chunks + ADRs + PRDs + plans. "+
				"Use this before implementing anything to ensure alignment with past decisions. "+
				"Returns code, architecture decisions, requirements, and plans together."),
			mcpgo.WithString("query",
				mcpgo.Required(),
				mcpgo.Description("What you are about to implement or change")),
			mcpgo.WithString("project_path",
				mcpgo.Description("Project path (defaults to workspace folder)")),
			mcpgo.WithNumber("limit",
				mcpgo.Description("Max results per source (default 3)")),
		),
		s.withMetrics("ciam_decision_context", "query", s.handleDecisionContext),
	)
}

func (s *Server) handleIndex(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	path, err := req.RequireString("project_path")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	ptype := req.GetString("project_type", "")

	result, err := s.client.Index(path, ptype)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	return mcpgo.NewToolResultText(fmt.Sprintf(
"Indexed %d chunks from %d files in %s (type: %s)",
result.ChunksIndexed, result.FilesProcessed, result.ProjectID, result.ProjectType,
)), nil
}

func (s *Server) handleSearch(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	projectPath := req.GetString("project_path", s.cfg.ProjectPath)
	chunkType := req.GetString("chunk_type", "")
	limit := req.GetInt("limit", 5)
	compress := req.GetBool("compress", false)

	projectID := filepath.Base(projectPath)
	results, err := s.client.Search(query, projectID, chunkType, limit, compress)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	data, _ := json.MarshalIndent(results.Chunks, "", "  ")
	return mcpgo.NewToolResultText(string(data)), nil
}

func (s *Server) handleRemember(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	content, err := req.RequireString("content")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	memType := req.GetString("type", "note")

	if err := s.client.Remember(content, memType); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	return mcpgo.NewToolResultText("Memory stored."), nil
}

func (s *Server) handleRecall(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	memories, err := s.client.Recall(query)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	data, _ := json.MarshalIndent(memories, "", "  ")
	return mcpgo.NewToolResultText(string(data)), nil
}

func (s *Server) handleCompress(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	content, err := req.RequireString("content")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	compressed := search.Compress(content)
	reduction := 0
	if len(content) > 0 {
		reduction = 100 - (len(compressed)*100)/len(content)
	}
	return mcpgo.NewToolResultText(
fmt.Sprintf("Compressed (%d%% reduction):\n\n%s", reduction, compressed),
), nil
}

func (s *Server) handleContext(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	projectPath := req.GetString("project_path", s.cfg.ProjectPath)
	chunkType := req.GetString("chunk_type", "")
	limit := req.GetInt("limit", 5)

	projectID := filepath.Base(projectPath)
	results, err := s.client.Search(query, projectID, chunkType, limit, true)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	data, _ := json.MarshalIndent(results.Chunks, "", "  ")
	return mcpgo.NewToolResultText(string(data)), nil
}

func (s *Server) handleDjangoMap(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	projectPath := req.GetString("project_path", s.cfg.ProjectPath)
	idx := djangoIndexer.New(projectPath, filepath.Base(projectPath))
	appMap, err := idx.AppMap()
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	data, _ := json.MarshalIndent(appMap, "", "  ")
	return mcpgo.NewToolResultText(string(data)), nil
}

// ── Knowledge management handlers (Fase 5) ──────────────────────────────────

func (s *Server) handleADRSearch(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	return s.docSearch(req, "adr")
}

func (s *Server) handlePRDSearch(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	return s.docSearch(req, "prd")
}

func (s *Server) handlePlanSearch(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	return s.docSearch(req, "plan")
}

func (s *Server) handleResearchSearch(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	return s.docSearch(req, "research")
}

// docSearch is a shared helper for single-type doc searches.
func (s *Server) docSearch(req mcpgo.CallToolRequest, chunkType string) (*mcpgo.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	projectPath := req.GetString("project_path", s.cfg.ProjectPath)
	projectID := filepath.Base(projectPath)
	limit := req.GetInt("limit", 5)

	results, err := s.client.Search(query, projectID, chunkType, limit, false)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	if len(results.Chunks) == 0 {
		return mcpgo.NewToolResultText(fmt.Sprintf(
			"No %s documents found for: %q\n\nTip: run `ciam index . --include-docs` after creating docs.", chunkType, query,
		)), nil
	}
	data, _ := json.MarshalIndent(results.Chunks, "", "  ")
	return mcpgo.NewToolResultText(string(data)), nil
}

// handleDecisionContext combines code + ADR + PRD + plan results for one query.
func (s *Server) handleDecisionContext(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	limit := req.GetInt("limit", 3)

	type section struct {
		label     string
		chunkType string
	}
	sections := []section{
		{"## Code", ""},
		{"## Architecture Decisions (ADR)", "adr"},
		{"## Requirements (PRD)", "prd"},
		{"## Implementation Plans", "plan"},
	}

	projectPath := req.GetString("project_path", s.cfg.ProjectPath)
	projectID := filepath.Base(projectPath)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Decision context for: %q\n\n", query))

	for _, sec := range sections {
		results, err := s.client.Search(query, projectID, sec.chunkType, limit, true)
		if err != nil {
			sb.WriteString(fmt.Sprintf("%s\n_Error: %v_\n\n", sec.label, err))
			continue
		}
		sb.WriteString(sec.label + "\n")
		if len(results.Chunks) == 0 {
			sb.WriteString("_No results_\n\n")
			continue
		}
		for _, c := range results.Chunks {
			sb.WriteString(fmt.Sprintf("**%s** (`%s`, score: %.2f)\n```\n%s\n```\n\n",
				c.FilePath, c.ChunkType, c.Score, c.Content))
		}
	}

	return mcpgo.NewToolResultText(sb.String()), nil
}
