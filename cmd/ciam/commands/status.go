package commands

import (
	"fmt"

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
		return nil
	},
}
