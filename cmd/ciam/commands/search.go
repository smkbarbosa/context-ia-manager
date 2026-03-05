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
	searchType     string
	searchLimit    int
	searchCompress bool
	searchProject  string
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Semantic + keyword search in the indexed project",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := strings.Join(args, " ")
		cfg := config.Load()
		client := api.NewClient(cfg.APIURL)

		// Derive project ID from --project flag, CIAM_PROJECT_PATH, or cwd.
		projectID := searchProject
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

		results, err := client.Search(query, projectID, searchType, searchLimit, searchCompress)
		if err != nil {
			return fmt.Errorf("search failed: %w", err)
		}

		if len(results.Chunks) == 0 {
			fmt.Printf("No results found for project %q.\n", projectID)
			fmt.Println("Tip: run `ciam index .` inside the project directory first.")
			return nil
		}

		fmt.Printf("Found %d results (project: %s):\n\n", len(results.Chunks), projectID)
		for i, chunk := range results.Chunks {
			fmt.Printf("─── %d. %s [%s] (score: %.2f)\n",
				i+1, chunk.FilePath, chunk.ChunkType, chunk.Score)
			fmt.Println(chunk.Content)
			fmt.Println()
		}
		return nil
	},
}

func init() {
	searchCmd.Flags().StringVarP(&searchType, "type", "t", "",
		"Filter by chunk type: model, view, url, serializer, task, test, generic")
	searchCmd.Flags().IntVarP(&searchLimit, "limit", "n", 5,
		"Maximum number of results")
	searchCmd.Flags().BoolVarP(&searchCompress, "compress", "c", false,
		"Compress results to reduce tokens")
	searchCmd.Flags().StringVarP(&searchProject, "project", "p", "",
		"Project ID to search (defaults to basename of current directory or CIAM_PROJECT_PATH)")
}
