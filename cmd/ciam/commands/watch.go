// Package commands — watch.go
// ciam watch polls the project directory and incrementally re-indexes
// any file whose content changed since the last check.
// No external dependencies — uses mtime+size polling.
package commands

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smkbarbosa/context-ia-manager/internal/api"
	"github.com/smkbarbosa/context-ia-manager/internal/config"
	"github.com/smkbarbosa/context-ia-manager/internal/indexer"
	djangoIndexer "github.com/smkbarbosa/context-ia-manager/internal/indexer/django"
	"github.com/spf13/cobra"
)

var watchInterval int

var watchCmd = &cobra.Command{
	Use:   "watch [path]",
	Short: "Watch project and re-index changed files automatically",
	Long: `Polls the project directory every N seconds and incrementally re-indexes
any file whose content has changed. Only re-embeds what actually changed,
so it is safe to leave running in the background.

Watched extensions: .py .go .md .txt .toml .yaml .yml .json .html .js .ts .sh

Example:
  ciam watch .
  ciam watch /path/to/autosaas --interval 5`,
	Args: cobra.MaximumNArgs(1),
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

		projectID := filepath.Base(absPath)
		projectType := indexer.DetectType(absPath)

		cfg := config.Load()
		client := api.NewClient(cfg.APIURL)

		interval := time.Duration(watchInterval) * time.Second

		fmt.Printf("Watching %s (project: %s, type: %s)\n", absPath, projectID, projectType)
		fmt.Printf("Polling every %s — Ctrl-C to stop\n\n", interval)

		// fileState tracks the last observed mtime+size for quick dirty detection.
		type fileState struct {
			mtime time.Time
			size  int64
		}
		seen := map[string]fileState{}

		for {
			changed := 0
			skipped := 0

			walkErr := filepath.WalkDir(absPath, func(path string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return nil
				}

				if d.IsDir() {
					name := d.Name()
					if strings.HasPrefix(name, ".") {
						return filepath.SkipDir
					}
					switch name {
					case "__pycache__", "node_modules", ".venv", "venv",
						"dist", "build", "htmlcov", "staticfiles", "media",
						".mypy_cache", ".pytest_cache", ".tox", "migrations":
						return filepath.SkipDir
					}
					return nil
				}

				ext := strings.ToLower(filepath.Ext(d.Name()))
				if !indexer.IndexableExtensions[ext] {
					return nil
				}

				info, err := d.Info()
				if err != nil {
					return nil
				}

				prev, alreadySeen := seen[path]
				cur := fileState{mtime: info.ModTime(), size: info.Size()}

				// Quick dirty check: mtime or size changed.
				if alreadySeen && prev.mtime.Equal(cur.mtime) && prev.size == cur.size {
					return nil
				}

				seen[path] = cur

				// First pass: don't re-index on startup, just mark as baseline.
				if !alreadySeen {
					return nil
				}

				// File changed — read and index.
				data, err := os.ReadFile(path)
				if err != nil {
					return nil
				}

				sum := sha256.Sum256(data)
				hash := fmt.Sprintf("%x", sum)

				rel, _ := filepath.Rel(absPath, path)
				chunkType := inferChunkType(rel, string(data), projectType)
				textChunks := indexer.FileChunk(rel, string(data), 50, 5)

				payload := make([]api.ChunkPayload, len(textChunks))
				for i, c := range textChunks {
					payload[i] = api.ChunkPayload{
						FilePath:  rel,
						ChunkType: chunkType,
						Content:   indexer.Truncate(c),
					}
				}

				result, err := client.IndexFile(projectID, hash, payload)
				if err != nil {
					fmt.Printf("[%s] ✗ %s — API error: %v\n",
						time.Now().Format("15:04:05"), rel, err)
					return nil
				}

				if result.Updated {
					fmt.Printf("[%s] ✓ %s (%s, %d chunks)\n",
						time.Now().Format("15:04:05"), rel, chunkType, result.ChunksIndexed)
					changed++
				} else {
					skipped++
				}
				return nil
			})

			if walkErr != nil {
				fmt.Printf("walk error: %v\n", walkErr)
			}

			if changed > 0 {
				fmt.Printf("[%s] — %d file(s) re-indexed, %d unchanged\n\n",
					time.Now().Format("15:04:05"), changed, skipped)
			}

			time.Sleep(interval)
		}
	},
}

// inferChunkType maps a relative file path + content to a chunk_type string.
// Reuses Django-specific logic when projectType is "django".
func inferChunkType(rel, content, projectType string) string {
	// Docs sub-directories take priority.
	lower := strings.ToLower(filepath.ToSlash(rel))
	switch {
	case strings.HasPrefix(lower, "docs/adr/"):
		return "adr"
	case strings.HasPrefix(lower, "docs/prd/"):
		return "prd"
	case strings.HasPrefix(lower, "docs/plans/"):
		return "plan"
	case strings.HasPrefix(lower, "docs/research/"):
		return "research"
	}

	if projectType == "django" {
		base := strings.ToLower(filepath.Base(rel))
		return djangoIndexer.InferType(base, content)
	}
	return "generic"
}

func init() {
	watchCmd.Flags().IntVar(&watchInterval, "interval", 2,
		"Polling interval in seconds (default 2)")
}
