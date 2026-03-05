// Package docsindexer reads a project's docs/ directory and produces
// storage chunks with chunk_type adr | prd | plan | research.
package docsindexer

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/smkbarbosa/context-ia-manager/internal/storage"
)

// subDirs maps docs/ sub-directory names to chunk_type values.
var subDirs = map[string]string{
	"adr":      "adr",
	"prd":      "prd",
	"plans":    "plan",
	"research": "research",
}

// Indexer walks docs/ and converts each .md file into a storage.Chunk.
type Indexer struct {
	projectRoot string
	projectID   string
}

// New returns a docs Indexer.
func New(projectRoot, projectID string) *Indexer {
	return &Indexer{projectRoot: projectRoot, projectID: projectID}
}

// Index reads all docs and returns chunks ready for embedding.
func (idx *Indexer) Index() ([]storage.Chunk, error) {
	var chunks []storage.Chunk

	for sub, chunkType := range subDirs {
		dir := filepath.Join(idx.projectRoot, "docs", sub)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue // sub-directory does not exist — skip silently
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}

		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			fpath := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(fpath)
			if err != nil {
				continue
			}
			content := strings.TrimSpace(string(data))
			if content == "" {
				continue
			}
			// Doc files are typically short enough to be a single chunk.
			// Relative path for display.
			rel, _ := filepath.Rel(idx.projectRoot, fpath)
			chunks = append(chunks, storage.Chunk{
				ProjectID: idx.projectID,
				FilePath:  rel,
				ChunkType: chunkType,
				Content:   content,
			})
		}
	}
	return chunks, nil
}
