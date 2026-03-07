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

// ciamInstructions is injected into the MCP initialize response.
// Every MCP-compatible client (Copilot, Claude, Cursor) reads this
// and uses it as a persistent system-level instruction set.
const ciamInstructions = `# ciam — Context IA Manager

You have access to the ciam toolkit. It provides semantic search, memory,
and knowledge management for this project. ALWAYS use ciam tools BEFORE
reading files, writing code, or making architectural decisions.

## MANDATORY WORKFLOW — follow this every time

### Before implementing anything
1. Call ciam_decision_context("<what you are about to do>")
   → returns code chunks + ADRs + PRDs + implementation plans in one call
   → use this result as your primary context source
2. Call ciam_recall("<topic>") to check for stored decisions/bugs/notes
3. Only THEN write code

### Before reading a file directly
1. Call ciam_search("<what you need>") first
   → if the result is sufficient, do NOT read the file
   → only read files when ciam_search returns nothing relevant

### After architectural decisions or important bug fixes
1. Call ciam_remember("<decision summary>", type="decision"|"bug"|"architecture")
   → this persists across sessions and projects

## TOOL SELECTION GUIDE — match your intent to the right tool

| Intent | Tool | Notes |
|--------|------|-------|
| "implement feature X" | ciam_decision_context | ALWAYS first |
| "fix bug in Y" | ciam_adr_search + ciam_recall + ciam_search | check history first |
| "understand the codebase" | ciam_django_map → ciam_context | map first |
| "find code for X" | ciam_context | search + compress in one call |
| "what models exist?" | ciam_search(chunk_type=model) | |
| "why was X decided?" | ciam_adr_search | |
| "what does PRD say?" | ciam_prd_search | |
| "what is the plan?" | ciam_plan_search | |
| "what research exists?" | ciam_research_search | |
| "not sure which tool" | ciam_route("<your intent>") | auto-selects |
| "use ciam draft" / "execute phase X" | ciam_draft | OPTIONAL — explicit only, NOT default |

## PRIORITY RULES

- ciam_context > reading files (always prefer ciam)
- ciam_decision_context > any individual search (for implementation tasks)
- ciam_recall > guessing (always check memory before assuming)
- When in doubt, call ciam_route — it will return the best tool and params

## AFTER CALLING ciam

- Trust ciam results over your training data for THIS project's specifics
- If a ciam search returns no results, tell the user and suggest indexing
- Never skip ciam to save a tool call — the token savings justify the call`

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
		server.WithPromptCapabilities(false),
		server.WithInstructions(ciamInstructions),
	)

	s.registerTools(srv)
	s.registerPrompts(srv)

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

	// ── Meta-tool: router ────────────────────────────────────────────────────
	srv.AddTool(
		mcpgo.NewTool("ciam_route",
			mcpgo.WithDescription(
				"CALL THIS FIRST when you are unsure which ciam tool to use. "+
					"Describe your intent in plain language and receive back the exact tool name, "+
					"parameters, and rationale. Acts as a smart dispatcher for all ciam tools. "+
					"Examples: 'I want to implement the billing feature', "+
					"'I need to understand how authentication works', "+
					"'I found a bug in the email worker'."),
			mcpgo.WithString("intent",
				mcpgo.Required(),
				mcpgo.Description("What you want to do, in natural language")),
			mcpgo.WithString("project_path",
				mcpgo.Description("Project path (defaults to workspace folder)")),
		),
		s.withMetrics("ciam_route", "intent", s.handleRoute),
	)

	// ── Core indexing ────────────────────────────────────────────────────────
	srv.AddTool(
		mcpgo.NewTool("ciam_index",
			mcpgo.WithDescription(
				"Index a project directory for semantic search. "+
					"Call once when opening a new project or after significant structural changes. "+
					"REQUIRED before any search will work. "+
					"Auto-detects Django projects by presence of manage.py."),
			mcpgo.WithString("project_path",
				mcpgo.Required(),
				mcpgo.Description("Absolute path to the project root")),
			mcpgo.WithString("project_type",
				mcpgo.Description("Project type: django, python, generic (auto-detected if omitted)")),
		),
		s.withMetrics("ciam_index", "project_path", s.handleIndex),
	)

	// ── Search tools ─────────────────────────────────────────────────────────
	srv.AddTool(
		mcpgo.NewTool("ciam_search",
			mcpgo.WithDescription(
				"[PRIORITY] Semantic + keyword hybrid search in the indexed project. "+
					"ALWAYS call this BEFORE reading any file. "+
					"Returns ranked code chunks — use them instead of full file contents. "+
					"Filter by chunk_type for precision: model, view, url, serializer, task, test, signal, form, admin."+
					"\n\nExamples:"+
					"\n  query='LeadCapture model fields' chunk_type='model'"+
					"\n  query='email sending task' chunk_type='task'"+
					"\n  query='JWT authentication' chunk_type='view'"),
			mcpgo.WithString("query",
				mcpgo.Required(),
				mcpgo.Description("Natural language or code search query")),
			mcpgo.WithString("project_path",
				mcpgo.Description("Project path (defaults to workspace folder)")),
			mcpgo.WithString("chunk_type",
				mcpgo.Description("Filter: model, view, url, serializer, task, test, signal, form, admin, generic")),
			mcpgo.WithNumber("limit",
				mcpgo.Description("Max results (default 5)")),
			mcpgo.WithBoolean("compress",
				mcpgo.Description("Compress results to reduce tokens (default false)")),
		),
		s.withMetrics("ciam_search", "query", s.handleSearch),
	)

	srv.AddTool(
		mcpgo.NewTool("ciam_context",
			mcpgo.WithDescription(
				"[PRIORITY — DEFAULT SEARCH TOOL] Search + compress in one call. "+
					"Maximum token efficiency. "+
					"Use this as your DEFAULT tool for finding any code context. "+
					"Prefer this over ciam_search when you don't need raw content. "+
					"Use ciam_decision_context instead when about to implement something."),
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

	// ── Decision context (most powerful) ─────────────────────────────────────
	srv.AddTool(
		mcpgo.NewTool("ciam_decision_context",
			mcpgo.WithDescription(
				"[HIGHEST PRIORITY — CALL BEFORE IMPLEMENTING ANYTHING] "+
					"Returns full decision context in one call: "+
					"relevant code chunks + Architecture Decisions (ADRs) + Product Requirements (PRDs) + Implementation Plans. "+
					"MANDATORY before writing any non-trivial code. "+
					"Ensures the AI respects past decisions, requirements, and planned approach. "+
					"Combine with ciam_recall for complete context."),
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

	// ── Django structural map ─────────────────────────────────────────────────
	srv.AddTool(
		mcpgo.NewTool("ciam_django_map",
			mcpgo.WithDescription(
				"Returns a structural map of the Django project: apps, models, views, urls, serializers. "+
					"Call this FIRST when onboarding to a Django project or when you need to understand its structure. "+
					"Use BEFORE ciam_search when you don't know which app or file to look in."),
			mcpgo.WithString("project_path",
				mcpgo.Description("Absolute path to the Django project root")),
		),
		s.withMetrics("ciam_django_map", "", s.handleDjangoMap),
	)

	// ── Knowledge: ADR, PRD, Plan, Research ──────────────────────────────────
	srv.AddTool(
		mcpgo.NewTool("ciam_adr_search",
			mcpgo.WithDescription(
				"Search Architecture Decision Records (ADRs) in docs/adr/. "+
					"ALWAYS call this before proposing architectural changes. "+
					"Explains WHY past decisions were made — prevents repeating mistakes. "+
					"Call when: 'should we change X?', 'why is Y implemented this way?', "+
					"'is there a record of the decision about Z?'"),
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
			mcpgo.WithDescription(
				"Search Product Requirement Documents (PRDs) in docs/prd/. "+
					"Call this before implementing any feature to understand WHAT it must do and its acceptance criteria. "+
					"Prevents building the wrong thing. "+
					"Call when: 'what should feature X do?', 'what are the acceptance criteria?', "+
					"'is there a PRD for this?'"),
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
			mcpgo.WithDescription(
				"Search implementation plans in docs/plans/. "+
					"Call before implementing a feature phase to understand the planned approach, "+
					"phased structure and success criteria. "+
					"Prevents implementing phase 2 before phase 1 is done. "+
					"Call when: 'what is the plan for X?', 'which phase are we in?', "+
					"'what are the success criteria?'"),
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
			mcpgo.WithDescription(
				"Search research documents in docs/research/. "+
					"Contains external knowledge, benchmarks, and technical research ingested into this project. "+
					"Call when: 'is there research on X?', 'what did we find out about Y library?', "+
					"'are there performance benchmarks for Z?'"),
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

	// ── Memory ───────────────────────────────────────────────────────────────
	srv.AddTool(
		mcpgo.NewTool("ciam_remember",
			mcpgo.WithDescription(
				"Store an important decision, note, bug, or architectural choice in persistent memory. "+
					"Available across sessions and projects. "+
					"CALL THIS after: any architectural decision, important bug fix, team agreement, "+
					"or any fact that would be costly to lose between sessions. "+
					"Memory persists forever — when in doubt, remember it."),
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
			mcpgo.WithDescription(
				"Search stored memories from previous sessions. "+
					"ALWAYS call this alongside ciam_decision_context before implementing anything. "+
					"Recovers decisions, bug notes, and architectural choices from past sessions. "+
					"Call when: 'have we decided X before?', 'is there a note about Y?', "+
					"'did we fix a bug related to Z?'"),
			mcpgo.WithString("query",
				mcpgo.Required(),
				mcpgo.Description("What you want to recall")),
		),
		s.withMetrics("ciam_recall", "query", s.handleRecall),
	)

	// ── Compression ──────────────────────────────────────────────────────────
	srv.AddTool(
		mcpgo.NewTool("ciam_compress",
			mcpgo.WithDescription(
				"Compress code: keeps function/class signatures, removes docstrings and comments. "+
					"Reduces token usage by 70-90%. "+
					"Use when you have code content and need to pass it to the AI with minimal tokens. "+
					"Prefer ciam_context (which auto-compresses) over calling this manually."),
			mcpgo.WithString("content",
				mcpgo.Required(),
				mcpgo.Description("Code content to compress")),
		),
		s.withMetrics("ciam_compress", "", s.handleCompress),
	)

	srv.AddTool(
		mcpgo.NewTool("ciam_draft",
			mcpgo.WithDescription(
				"OPTIONAL: Generate a speculative code draft via local Ollama LLM. "+
					"Uses project code chunks, ADRs and an optional plan phase to build a contextualised prompt, "+
					"then calls Ollama /api/generate. "+
					"NOT part of the default workflow — call this ONLY when the user explicitly says "+
					"\"use ciam draft\", \"generate a draft\", or \"execute phase X of plan\". "+
					"Requires CIAM_CODE_MODEL env var set on the server (e.g. qwen2.5-coder:1.5b)."),
			mcpgo.WithString("intent",
				mcpgo.Required(),
				mcpgo.Description("What to generate, e.g. \"Django view for user registration\"")),
			mcpgo.WithString("project_path",
				mcpgo.Description("Project path (defaults to workspace)")),
			mcpgo.WithString("chunk_type",
				mcpgo.Description("Filter code context by chunk type: model, view, task, serializer, …")),
			mcpgo.WithString("plan_id",
				mcpgo.Description("Plan ID to include as reference (e.g. Plan-001)")),
			mcpgo.WithString("phase",
				mcpgo.Description("Plan phase to reference (e.g. Fase 2)")),
		),
		s.withMetrics("ciam_draft", "intent", s.handleDraft),
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

// routeEntry maps an intent pattern (keyword list) to a tool recommendation.
type routeEntry struct {
	keywords []string
	tool     string
	params   string // hint for params
	reason   string
}

var routeTable = []routeEntry{
	{[]string{"implement", "add feature", "create feature", "build", "develop"}, "ciam_decision_context", `{"query": "<feature name>"}`, "Full decision context before implementing: code + ADRs + PRDs + plans"},
	{[]string{"bug", "fix", "error", "broken", "crash", "exception", "traceback"}, "ciam_recall + ciam_adr_search", `{"query": "<bug topic>"}`, "Check memory and ADRs for past decisions related to this bug"},
	{[]string{"understand", "how does", "what is", "explain", "overview"}, "ciam_context", `{"query": "<topic>"}`, "Search + compress for maximum token efficiency"},
	{[]string{"structure", "map", "apps", "models list", "onboard", "django map"}, "ciam_django_map", `{}`, "Structural map of the Django project"},
	{[]string{"model", "schema", "field", "database"}, "ciam_search", `{"query": "<model name>", "chunk_type": "model"}`, "Filtered model search"},
	{[]string{"view", "endpoint", "api", "request", "response"}, "ciam_search", `{"query": "<intent>", "chunk_type": "view"}`, "Filtered view/endpoint search"},
	{[]string{"task", "celery", "worker", "queue", "async"}, "ciam_search", `{"query": "<task name>", "chunk_type": "task"}`, "Filtered task search"},
	{[]string{"architecture", "decision", "adr", "why was", "why is"}, "ciam_adr_search", `{"query": "<topic>"}`, "Architecture decisions history"},
	{[]string{"requirement", "prd", "acceptance", "feature spec", "product"}, "ciam_prd_search", `{"query": "<feature>"}`, "Product requirements"},
	{[]string{"plan", "phase", "roadmap", "milestone"}, "ciam_plan_search", `{"query": "<feature>"}`, "Implementation plans"},
	{[]string{"ciam draft", "draft", "generate draft", "executar fase", "execute phase", "speculative"}, "ciam_draft", `{"intent": "<what to implement>", "plan_id": "<Plan-ID>", "phase": "<Fase N>"}`, "Optional: generate local code draft via Ollama — only when user explicitly requests it"},
	{[]string{"research", "benchmark", "library comparison", "external"}, "ciam_research_search", `{"query": "<topic>"}`, "Research docs"},
	{[]string{"remember", "store", "save decision", "note"}, "ciam_remember", `{"content": "<what to remember>", "type": "decision"}`, "Persist a decision or note"},
	{[]string{"recall", "past decision", "previous", "history"}, "ciam_recall", `{"query": "<topic>"}`, "Retrieve stored memory"},
}

func (s *Server) handleRoute(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	intent, err := req.RequireString("intent")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	lower := strings.ToLower(intent)

	for _, r := range routeTable {
		for _, kw := range r.keywords {
			if strings.Contains(lower, kw) {
				result := map[string]string{
					"tool":   r.tool,
					"params": r.params,
					"reason": r.reason,
					"intent": intent,
				}
				data, _ := json.MarshalIndent(result, "", "  ")
				return mcpgo.NewToolResultText(string(data)), nil
			}
		}
	}

	// Default fallback
	result := map[string]string{
		"tool":   "ciam_decision_context",
		"params": fmt.Sprintf(`{"query": %q}`, intent),
		"reason": "Default: full decision context is the safest starting point for any task",
		"intent": intent,
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return mcpgo.NewToolResultText(string(data)), nil
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

func (s *Server) handleDraft(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	intent, err := req.RequireString("intent")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	projectPath := req.GetString("project_path", s.cfg.ProjectPath)
	projectID := filepath.Base(projectPath)

	resp, err := s.client.Draft(api.DraftRequest{
		ProjectID: projectID,
		Intent:    intent,
		ChunkType: req.GetString("chunk_type", ""),
		PlanID:    req.GetString("plan_id", ""),
		Phase:     req.GetString("phase", ""),
		MaxTokens: req.GetInt("max_tokens", 512),
	})
	if err != nil {
		return mcpgo.NewToolResultError("ciam_draft: " + err.Error()), nil
	}

	var sb strings.Builder
	sb.WriteString("# ciam_draft — Rascunho gerado por Ollama\n\n")
	if resp.PlanExcerpt != "" {
		sb.WriteString("## Plano referenciado\n\n")
		sb.WriteString(resp.PlanExcerpt)
		sb.WriteString("\n\n---\n\n")
	}
	sb.WriteString("## Rascunho de código\n\n```\n")
	sb.WriteString(resp.Draft)
	sb.WriteString("\n```\n\n")
	sb.WriteString(fmt.Sprintf(
		"_Modelo: %s | Tokens prompt: ~%d | Tokens draft: ~%d | Contexto: %d arquivo(s)_\n\n",
		resp.ModelUsed, resp.TokensInPrompt, resp.TokensInDraft, len(resp.ContextUsed),
	))
	sb.WriteString("**IMPORTANTE**: Este é um rascunho especulativo gerado localmente.\n" +
		"Valide contra o plano e os ADRs antes de aceitar o código.\n")

	return mcpgo.NewToolResultText(sb.String()), nil
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

// registerPrompts registers MCP prompt templates. Prompts give the AI a structured
// workflow to follow for common tasks — the AI can pick the right prompt
// based on the user's intent, and the prompt walks it through the ciam calls.
func (s *Server) registerPrompts(srv *server.MCPServer) {
	srv.AddPrompt(
		mcpgo.NewPrompt("implement-feature",
			mcpgo.WithPromptDescription(
				"Guided workflow for implementing a new feature using ciam. "+
					"Calls decision_context, recall, and gives a structured plan."),
			mcpgo.WithArgument("feature",
				mcpgo.ArgumentDescription("The feature you want to implement"),
				mcpgo.RequiredArgument()),
			mcpgo.WithArgument("project_path",
				mcpgo.ArgumentDescription("Project path (defaults to current workspace)")),
		),
		func(_ context.Context, req mcpgo.GetPromptRequest) (*mcpgo.GetPromptResult, error) {
			feature := req.Params.Arguments["feature"]
			projectPath := req.Params.Arguments["project_path"]
			if projectPath == "" {
				projectPath = s.cfg.ProjectPath
			}
			return &mcpgo.GetPromptResult{
				Description: "Implement feature: " + feature,
				Messages: []mcpgo.PromptMessage{
					{
						Role: mcpgo.RoleUser,
						Content: mcpgo.NewTextContent(fmt.Sprintf(
							"I want to implement: %s\n\nProject: %s\n\n"+
								"Please follow this workflow:\n"+
								"1. Call ciam_decision_context(query=%q, project_path=%q) — get code + ADRs + PRDs + plans\n"+
								"2. Call ciam_recall(query=%q) — check stored decisions\n"+
								"3. Based on the results, describe what already exists and what needs to be built\n"+
								"4. Propose a minimal implementation plan aligned with the PRD and ADRs\n"+
								"5. Only then start writing code",
							feature, projectPath, feature, projectPath, feature,
						)),
					},
				},
			}, nil
		},
	)

	srv.AddPrompt(
		mcpgo.NewPrompt("fix-bug",
			mcpgo.WithPromptDescription(
				"Guided debug workflow: check memory, ADRs, and code context before proposing a fix."),
			mcpgo.WithArgument("bug",
				mcpgo.ArgumentDescription("Description of the bug or error"),
				mcpgo.RequiredArgument()),
			mcpgo.WithArgument("project_path",
				mcpgo.ArgumentDescription("Project path")),
		),
		func(_ context.Context, req mcpgo.GetPromptRequest) (*mcpgo.GetPromptResult, error) {
			bug := req.Params.Arguments["bug"]
			projectPath := req.Params.Arguments["project_path"]
			if projectPath == "" {
				projectPath = s.cfg.ProjectPath
			}
			return &mcpgo.GetPromptResult{
				Description: "Fix bug: " + bug,
				Messages: []mcpgo.PromptMessage{
					{
						Role: mcpgo.RoleUser,
						Content: mcpgo.NewTextContent(fmt.Sprintf(
							"Bug report: %s\n\nProject: %s\n\n"+
								"Please follow this workflow:\n"+
								"1. Call ciam_recall(query=%q) — check if we've seen this before\n"+
								"2. Call ciam_adr_search(query=%q) — check if there's an architectural note\n"+
								"3. Call ciam_search(query=%q) — find the relevant code\n"+
								"4. Diagnose the root cause using results\n"+
								"5. Propose a fix and call ciam_remember() with the diagnosis if it's non-obvious",
							bug, projectPath, bug, bug, bug,
						)),
					},
				},
			}, nil
		},
	)

	srv.AddPrompt(
		mcpgo.NewPrompt("understand-codebase",
			mcpgo.WithPromptDescription(
				"Onboarding workflow: map the project structure and search for key concepts."),
			mcpgo.WithArgument("topic",
				mcpgo.ArgumentDescription("What you want to understand (leave blank for full overview)")),
			mcpgo.WithArgument("project_path",
				mcpgo.ArgumentDescription("Project path")),
		),
		func(_ context.Context, req mcpgo.GetPromptRequest) (*mcpgo.GetPromptResult, error) {
			topic := req.Params.Arguments["topic"]
			projectPath := req.Params.Arguments["project_path"]
			if projectPath == "" {
				projectPath = s.cfg.ProjectPath
			}
			msg := fmt.Sprintf(
				"I want to understand the codebase%s.\n\nProject: %s\n\n"+
					"Please follow this workflow:\n"+
					"1. Call ciam_django_map(project_path=%q) — get the app/model/view structure\n"+
					"2. Call ciam_context(query=%q) — find compressed relevant code\n"+
					"3. Summarize what you found: apps, key models, main entry points, patterns used",
				func() string {
					if topic != "" {
						return ": " + topic
					}
					return ""
				}(),
				projectPath, projectPath,
				func() string {
					if topic != "" {
						return topic
					}
					return "project overview main models views"
				}(),
			)
			return &mcpgo.GetPromptResult{
				Description: "Understand codebase",
				Messages: []mcpgo.PromptMessage{
					{Role: mcpgo.RoleUser, Content: mcpgo.NewTextContent(msg)},
				},
			}, nil
		},
	)

	srv.AddPrompt(
		mcpgo.NewPrompt("review-changes",
			mcpgo.WithPromptDescription(
				"Review changes in context: pull decision context and check alignment with ADRs and PRDs."),
			mcpgo.WithArgument("change",
				mcpgo.ArgumentDescription("What was changed or what diff you want reviewed"),
				mcpgo.RequiredArgument()),
			mcpgo.WithArgument("project_path",
				mcpgo.ArgumentDescription("Project path")),
		),
		func(_ context.Context, req mcpgo.GetPromptRequest) (*mcpgo.GetPromptResult, error) {
			change := req.Params.Arguments["change"]
			projectPath := req.Params.Arguments["project_path"]
			if projectPath == "" {
				projectPath = s.cfg.ProjectPath
			}
			return &mcpgo.GetPromptResult{
				Description: "Review: " + change,
				Messages: []mcpgo.PromptMessage{
					{
						Role: mcpgo.RoleUser,
						Content: mcpgo.NewTextContent(fmt.Sprintf(
							"Please review: %s\n\nProject: %s\n\n"+
								"Workflow:\n"+
								"1. Call ciam_decision_context(query=%q) — understand what was intended\n"+
								"2. Call ciam_recall(query=%q) — check for stored notes\n"+
								"3. Review the change against the PRDs and ADRs from step 1\n"+
								"4. Flag any deviations from requirements or past decisions\n"+
								"5. Suggest improvements aligned with ciam context",
							change, projectPath, change, change,
						)),
					},
				},
			}, nil
		},
	)
}
