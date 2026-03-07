package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/smkbarbosa/context-ia-manager/internal/api"
	"github.com/smkbarbosa/context-ia-manager/internal/config"
	"github.com/spf13/cobra"
)

var (
	draftChunkType string
	draftPlanID    string
	draftPhase     string
	draftMaxTokens int
	draftProject   string
)

var draftCmd = &cobra.Command{
	Use:   "draft <intent>",
	Short: "Generate a speculative code draft using Ollama (requires CIAM_CODE_MODEL)",
	Long: `ciam draft generates a local code draft via Ollama, enriched with your
project's indexed code chunks, ADRs and, optionally, an implementation plan.

This feature is OPTIONAL and NOT part of the default workflow.
Activate it explicitly via this command or via the ciam_draft MCP tool.

Requires CIAM_CODE_MODEL to be set (e.g. export CIAM_CODE_MODEL=qwen2.5-coder:1.5b).
The model must be installed in Ollama (run: ollama pull qwen2.5-coder:1.5b).`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		intent := strings.Join(args, " ")
		cfg := config.Load()

		if cfg.CodeModel == "" {
			return fmt.Errorf(
				"CIAM_CODE_MODEL is not set\n" +
					"  Set it to a model installed in Ollama, e.g.:\n" +
					"    export CIAM_CODE_MODEL=qwen2.5-coder:1.5b\n" +
					"  Then install it:\n" +
					"    docker compose exec ollama ollama pull qwen2.5-coder:1.5b",
			)
		}

		// Resolve project ID from --project flag, CIAM_PROJECT_PATH, or cwd.
		projectID := draftProject
		if projectID == "" {
			base := cfg.ProjectPath
			if base == "" || base == "." {
				cwd, err := os.Getwd()
				if err == nil {
					base = cwd
				}
			}
			projectID = filepath.Base(base)
		}

		client := api.NewClient(cfg.APIURL)

		fmt.Printf("Generating draft for project %q...\n", projectID)
		fmt.Printf("  Intent:  %s\n", intent)
		if draftPlanID != "" {
			fmt.Printf("  Plan:    %s", draftPlanID)
			if draftPhase != "" {
				fmt.Printf(" / %s", draftPhase)
			}
			fmt.Println()
		}
		fmt.Printf("  Model:   %s\n\n", cfg.CodeModel)

		resp, err := client.Draft(api.DraftRequest{
			ProjectID: projectID,
			Intent:    intent,
			ChunkType: draftChunkType,
			PlanID:    draftPlanID,
			Phase:     draftPhase,
			MaxTokens: draftMaxTokens,
		})
		if err != nil {
			return fmt.Errorf("draft failed: %w", err)
		}

		fmt.Println("─── Draft ──────────────────────────────────────────────────────────")
		fmt.Println(resp.Draft)
		fmt.Println()

		if resp.PlanExcerpt != "" {
			fmt.Println("─── Plan excerpt used ───────────────────────────────────────────────")
			fmt.Println(resp.PlanExcerpt)
			fmt.Println()
		}

		fmt.Printf("─── Stats ───────────────────────────────────────────────────────────\n")
		fmt.Printf("  Model:           %s\n", resp.ModelUsed)
		fmt.Printf("  Prompt tokens:   ~%d\n", resp.TokensInPrompt)
		fmt.Printf("  Draft tokens:    ~%d\n", resp.TokensInDraft)
		fmt.Printf("  Context used:    %d files\n", len(resp.ContextUsed))
		for _, f := range resp.ContextUsed {
			fmt.Printf("    - %s\n", f)
		}

		return nil
	},
}

func init() {
	draftCmd.Flags().StringVarP(&draftChunkType, "type", "t", "",
		"Filter code context by chunk type (model, view, task, serializer, …)")
	draftCmd.Flags().StringVar(&draftPlanID, "plan", "",
		"Plan ID to include as reference (e.g. Plan-001)")
	draftCmd.Flags().StringVar(&draftPhase, "phase", "",
		"Plan phase to reference (e.g. \"Fase 2\")")
	draftCmd.Flags().IntVar(&draftMaxTokens, "max-tokens", 512,
		"Maximum tokens to generate")
	draftCmd.Flags().StringVarP(&draftProject, "project", "p", "",
		"Project ID (defaults to basename of current directory or CIAM_PROJECT_PATH)")
}
