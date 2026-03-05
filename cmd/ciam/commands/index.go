package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/smkbarbosa/context-ia-manager/internal/api"
	"github.com/smkbarbosa/context-ia-manager/internal/config"
	"github.com/smkbarbosa/context-ia-manager/internal/indexer"
	djangoIndexer "github.com/smkbarbosa/context-ia-manager/internal/indexer/django"
	"github.com/spf13/cobra"
)

var (
	indexType string
)

var indexCmd = &cobra.Command{
	Use:   "index [path]",
	Short: "Index a project directory for semantic search",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath := "."
		if len(args) > 0 {
			projectPath = args[0]
		}

		absPath, err := filepath.Abs(projectPath)
		if err != nil {
			return fmt.Errorf("invalid path: %w", err)
		}

		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			return fmt.Errorf("path does not exist: %s", absPath)
		}

		if indexType == "" {
			indexType = indexer.DetectType(absPath)
			fmt.Printf("Detected project type: %s\n", indexType)
		}

		projectID := filepath.Base(absPath)

		// File reading happens here on the HOST — the API (Docker) only gets text chunks.
		fmt.Printf("Reading files from %s (type: %s)...\n", absPath, indexType)
		var storageChunks []struct {
			FilePath  string
			ChunkType string
			Content   string
		}

		if indexType == "django" {
			idx := djangoIndexer.New(absPath, projectID)
			sc, err := idx.Index()
			if err != nil {
				return fmt.Errorf("indexing failed: %w", err)
			}
			for _, c := range sc {
				storageChunks = append(storageChunks, struct {
					FilePath  string
					ChunkType string
					Content   string
				}{c.FilePath, c.ChunkType, c.Content})
			}
		} else {
			idx := indexer.New(absPath, projectID)
			sc, err := idx.Index()
			if err != nil {
				return fmt.Errorf("indexing failed: %w", err)
			}
			for _, c := range sc {
				storageChunks = append(storageChunks, struct {
					FilePath  string
					ChunkType string
					Content   string
				}{c.FilePath, c.ChunkType, c.Content})
			}
		}

		if len(storageChunks) == 0 {
			fmt.Println("No indexable files found.")
			return nil
		}

		// Convert to API payload
		payload := make([]api.ChunkPayload, len(storageChunks))
		for i, c := range storageChunks {
			payload[i] = api.ChunkPayload{
				FilePath:  c.FilePath,
				ChunkType: c.ChunkType,
				Content:   c.Content,
			}
		}

		fmt.Printf("Sending %d chunks to API for embedding...\n", len(payload))
		cfg := config.Load()
		client := api.NewClient(cfg.APIURL)

		result, err := client.IndexChunks(projectID, indexType, payload)
		if err != nil {
			return fmt.Errorf("embedding/storage failed: %w", err)
		}

		fmt.Printf("Done. Indexed %d chunks from %d files.\n",
			result.ChunksIndexed, result.FilesProcessed)
		return nil
	},
}

func init() {
	indexCmd.Flags().StringVarP(&indexType, "type", "t", "",
		"Project type: django, python, generic (auto-detected if not set)")
}
