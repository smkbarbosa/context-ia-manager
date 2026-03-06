package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/smkbarbosa/context-ia-manager/internal/api"
	"github.com/smkbarbosa/context-ia-manager/internal/config"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show ciam service status and metrics",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		client := api.NewClient(cfg.APIURL)

		status, err := client.Status()
		if err != nil {
			return fmt.Errorf("could not reach ciam API at %s — is it running? (try: ciam up)\n%w",
				cfg.APIURL, err)
		}

		fmt.Println("ciam status")
		fmt.Printf("  API URL:        %s\n", cfg.APIURL)
		fmt.Printf("  Ollama URL:     %s\n", cfg.OllamaURL)
		fmt.Printf("  Projects:       %d indexed\n", status.ProjectsIndexed)
		fmt.Printf("  Total chunks:   %d\n", status.TotalChunks)
		fmt.Printf("  Memories:       %d\n", status.MemoriesStored)
		fmt.Printf("  Cache hits:     %d\n", status.CacheHits)

		// Token savings block.
		fmt.Println()
		fmt.Println("  Token savings:")
		fmt.Printf("    Project size (est.):  ~%d tokens\n", status.TotalProjectTokens)
		fmt.Printf("    Served via search:     %d tokens\n", status.TokensServedViaSearch)
		if status.TotalProjectTokens > 0 && status.TokensServedViaSearch > 0 {
			pct := 100 - (status.TokensServedViaSearch*100)/status.TotalProjectTokens
			fmt.Printf("    Reduction:            ~%d%% fewer tokens to the AI\n", pct)
		} else if status.TotalProjectTokens > 0 {
			fmt.Printf("    Reduction:            run a search to start tracking\n")
		}
		fmt.Printf("    Estimated saved:      ~%d tokens total\n", status.EstimatedTokensSaved)

		// Per-project breakdown.
		if len(status.Projects) > 0 {
			fmt.Println()
			fmt.Println("  Projects:")
			fmt.Printf("  %-24s  %7s  %7s  %10s  %-26s  %s\n",
				"PROJECT", "CHUNKS", "FILES", "~TOKENS", "LAST INDEXED", "TYPES")
			fmt.Printf("  %s\n", strings.Repeat("-", 100))
			for _, p := range status.Projects {
				// Sort and join type counts: model:12 view:8 …
				keys := make([]string, 0, len(p.ChunkTypes))
				for k := range p.ChunkTypes {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				parts := make([]string, 0, len(keys))
				for _, k := range keys {
					parts = append(parts, fmt.Sprintf("%s:%d", k, p.ChunkTypes[k]))
				}
				types := strings.Join(parts, " ")
				if len(types) > 40 {
					types = types[:37] + "..."
				}

				// Trim last_indexed to date+time without seconds.
				indexedAt := p.LastIndexed
				if len(indexedAt) > 16 {
					indexedAt = indexedAt[:16]
				}

				fmt.Printf("  %-24s  %7d  %7d  %10d  %-26s  %s\n",
					p.ProjectID, p.TotalChunks, p.TotalFiles, p.TokenEstimate, indexedAt, types)
			}
		}

		if len(status.MCPStats) > 0 {
			fmt.Println()
			fmt.Println("  MCP tool usage:")
			fmt.Printf("  %-28s  %8s  %6s  %10s  %s\n", "FERRAMENTA", "CHAMADAS", "ERROS", "LAT.MÉD.", "ÚLTIMA CHAMADA")
			fmt.Printf("  %s\n", strings.Repeat("-", 80))
			for _, t := range status.MCPStats {
				fmt.Printf("  %-28s  %8d  %6d  %8.0fms  %s\n",
					t.ToolName, t.CallCount, t.ErrorCount, t.AvgLatencyMs, t.LastCalledAt)
			}
			fmt.Printf("\n  Status page: %s/status\n", cfg.APIURL)
		}
		return nil
	},
}
