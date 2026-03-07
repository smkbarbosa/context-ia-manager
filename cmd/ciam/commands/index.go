package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/smkbarbosa/context-ia-manager/internal/api"
	"github.com/smkbarbosa/context-ia-manager/internal/config"
	"github.com/smkbarbosa/context-ia-manager/internal/indexer"
	djangoIndexer "github.com/smkbarbosa/context-ia-manager/internal/indexer/django"
	docsIndexer "github.com/smkbarbosa/context-ia-manager/internal/indexer/docs"
	"github.com/spf13/cobra"
)

var (
	indexType   string
	includeDocs bool
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

		// If --include-docs is set, also index docs/ directory.
		if includeDocs {
			dIdx := docsIndexer.New(absPath, projectID)
			dChunks, err := dIdx.Index()
			if err != nil {
				fmt.Printf("Warning: docs indexing failed: %v\n", err)
			} else if len(dChunks) > 0 {
				fmt.Printf("Found %d doc chunks (ADR/PRD/plan/research)\n", len(dChunks))
				for _, c := range dChunks {
					storageChunks = append(storageChunks, struct {
						FilePath  string
						ChunkType string
						Content   string
					}{c.FilePath, c.ChunkType, c.Content})
				}
			} else {
				fmt.Println("No docs found (docs/adr, docs/prd, docs/plans, docs/research).")
			}
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

		cfg := config.Load()
		client := api.NewClient(cfg.APIURL)

		total := len(payload)
		const batchSize = 50
		batches := (total + batchSize - 1) / batchSize

		fmt.Printf("Embedding %d chunks in %d batches (batch size=%d)...\n", total, batches, batchSize)

		var totalIndexed, totalFiles int
		for b := 0; b < batches; b++ {
			start := b * batchSize
			end := start + batchSize
			if end > total {
				end = total
			}
			fmt.Printf("  [%d/%d] chunks %d–%d... ", b+1, batches, start+1, end)

			// reset=true only on the first batch so the server clears stale
			// data before the new index; subsequent batches just append.
			result, err := client.IndexChunks(projectID, indexType, payload[start:end], b == 0)
			if err != nil {
				fmt.Println("FAILED")
				return fmt.Errorf("batch %d failed: %w", b+1, err)
			}
			totalIndexed += result.ChunksIndexed
			totalFiles += result.FilesProcessed
			fmt.Printf("OK (%d indexed)\n", result.ChunksIndexed)
		}

		fmt.Printf("\nDone. Indexed %d chunks from %d files.\n", totalIndexed, totalFiles)
		return nil
	},
}

func init() {
	indexCmd.Flags().StringVarP(&indexType, "type", "t", "",
		"Project type: django, python, generic (auto-detected if not set)")
	indexCmd.Flags().BoolVar(&includeDocs, "include-docs", false,
		"Also index docs/ directory (ADR, PRD, plans, research)")
}
