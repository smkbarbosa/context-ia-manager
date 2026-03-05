package commands

import (
	"fmt"
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
		fmt.Printf("  Tokens saved:   ~%d\n", status.EstimatedTokensSaved)

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
