package commands

import (
	"fmt"
	"strings"

	"github.com/smkbarbosa/context-ia-manager/internal/api"
	"github.com/smkbarbosa/context-ia-manager/internal/config"
	"github.com/spf13/cobra"
)

var (
	searchType    string
	searchLimit   int
	searchCompress bool
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Semantic + keyword search in the indexed project",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := strings.Join(args, " ")
		cfg := config.Load()
		client := api.NewClient(cfg.APIURL)

		results, err := client.Search(query, searchType, searchLimit, searchCompress)
		if err != nil {
			return fmt.Errorf("search failed: %w", err)
		}

		if len(results.Chunks) == 0 {
			fmt.Println("No results found.")
			return nil
		}

		fmt.Printf("Found %d results:\n\n", len(results.Chunks))
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
}
