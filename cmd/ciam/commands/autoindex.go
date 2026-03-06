// Package commands — autoindex.go
// autoIndexDoc reads a newly created document and immediately sends it to
// the ciam API for embedding. This ensures search works right after creation,
// without requiring a full `ciam index .` run.
package commands

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"github.com/smkbarbosa/context-ia-manager/internal/api"
	"github.com/smkbarbosa/context-ia-manager/internal/config"
	"github.com/smkbarbosa/context-ia-manager/internal/indexer"
)

// autoIndexDoc indexes a single file produced by a doc command (adr/prd/plan/research).
// projectRoot is the project root; filePath is the absolute path to the created file;
// chunkType is one of: adr, prd, plan, research.
//
// It prints a brief status line and returns without blocking — errors are printed
// as warnings so they never abort the doc-creation flow.
func autoIndexDoc(projectRoot, filePath, chunkType string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Printf("  ⚠ auto-index: could not read file: %v\n", err)
		return
	}

	content := string(data)
	if len(content) == 0 {
		return
	}

	// Compute SHA-256 so the server can skip re-embedding unchanged files.
	sum := sha256.Sum256(data)
	hash := fmt.Sprintf("%x", sum)

	rel, _ := filepath.Rel(projectRoot, filePath)
	projectID := filepath.Base(projectRoot)

	// Chunk the document (docs are usually short; single chunk is fine).
	textChunks := indexer.FileChunk(rel, content, 50, 5)
	payload := make([]api.ChunkPayload, len(textChunks))
	for i, c := range textChunks {
		payload[i] = api.ChunkPayload{
			FilePath:  rel,
			ChunkType: chunkType,
			Content:   indexer.Truncate(c),
		}
	}

	cfg := config.Load()
	client := api.NewClient(cfg.APIURL)

	result, err := client.IndexFile(projectID, hash, payload)
	if err != nil {
		fmt.Printf("  ⚠ auto-index: API unreachable (%v) — run `ciam index . --include-docs` later\n", err)
		return
	}
	if result.Updated {
		fmt.Printf("  ✓ Indexed %d chunk(s) → searchable now\n", result.ChunksIndexed)
	} else {
		fmt.Printf("  ✓ Already indexed (content unchanged)\n")
	}
}
