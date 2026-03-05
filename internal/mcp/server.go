// Package mcp implements the MCP stdio server that exposes ciam tools
// to VSCode, Antigravity, and any other MCP-compatible client.
package mcp

import (
"context"
"encoding/json"
"fmt"
"path/filepath"

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
		s.handleIndex,
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
		s.handleSearch,
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
		s.handleRemember,
	)

	srv.AddTool(
mcpgo.NewTool("ciam_recall",
mcpgo.WithDescription("Search stored memories from previous sessions."),
mcpgo.WithString("query",
mcpgo.Required(),
				mcpgo.Description("What you want to recall")),
		),
		s.handleRecall,
	)

	srv.AddTool(
mcpgo.NewTool("ciam_compress",
mcpgo.WithDescription("Compress code: keeps signatures, removes docstrings/comments. "+
"Reduces tokens by 70-90%."),
mcpgo.WithString("content",
mcpgo.Required(),
				mcpgo.Description("Code content to compress")),
		),
		s.handleCompress,
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
		s.handleContext,
	)

	srv.AddTool(
mcpgo.NewTool("ciam_django_map",
mcpgo.WithDescription("Returns a structural map of the Django project: "+
"apps, models, views, urls, serializers. Great for onboarding."),
mcpgo.WithString("project_path",
mcpgo.Description("Absolute path to the Django project root")),
),
s.handleDjangoMap,
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

	_ = filepath.Base(projectPath)
	results, err := s.client.Search(query, chunkType, limit, compress)
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

	_ = filepath.Base(projectPath)
	results, err := s.client.Search(query, chunkType, limit, true)
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
