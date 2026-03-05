// Package indexer walks a project directory, splits files into chunks,
// and detects project type automatically.
package indexer

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/smkbarbosa/context-ia-manager/internal/storage"
)

// ProjectType identifies the kind of project being indexed.
type ProjectType string

const (
	TypeDjango  ProjectType = "django"
	TypePython  ProjectType = "python"
	TypeGeneric ProjectType = "generic"
)

// DetectType inspects the directory to guess the project type.
func DetectType(root string) string {
	markers := map[string]string{
		"manage.py": string(TypeDjango),
		"settings.py": string(TypeDjango),
	}

	found := string(TypeGeneric)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		base := d.Name()
		if t, ok := markers[base]; ok {
			found = t
			return filepath.SkipAll
		}
		return nil
	})

	// If it's a Python project but no Django markers
	if found == string(TypeGeneric) {
		if _, err := filepath.Glob(filepath.Join(root, "*.py")); err == nil {
			found = string(TypePython)
		}
	}
	return found
}

// skipDirs are directories always excluded during indexing.
var skipDirs = map[string]bool{
	".git": true, "__pycache__": true, ".venv": true, "venv": true,
	"node_modules": true, ".mypy_cache": true, ".pytest_cache": true,
	"dist": true, "build": true, ".tox": true, "htmlcov": true,
}

// IndexableExtensions are file extensions considered for indexing.
var IndexableExtensions = map[string]bool{
	".py": true, ".go": true, ".md": true, ".txt": true,
	".toml": true, ".yaml": true, ".yml": true, ".json": true,
	".html": true, ".js": true, ".ts": true, ".sh": true,
}

// FileChunk splits a file into overlapping chunks of maxLines each.
func FileChunk(path, content string, maxLines, overlap int) []string {
	lines := strings.Split(content, "\n")
	var chunks []string
	for start := 0; start < len(lines); start += maxLines - overlap {
		end := start + maxLines
		if end > len(lines) {
			end = len(lines)
		}
		chunk := strings.Join(lines[start:end], "\n")
		if strings.TrimSpace(chunk) != "" {
			chunks = append(chunks, chunk)
		}
		if end == len(lines) {
			break
		}
	}
	return chunks
}

// Indexer is the base generic file indexer.
type Indexer struct {
	Root      string
	ProjectID string
}

// New creates a generic indexer for the given root.
func New(root, projectID string) *Indexer {
	return &Indexer{Root: root, ProjectID: projectID}
}

// Index walks root and returns all chunks ready for embedding.
func (idx *Indexer) Index() ([]storage.Chunk, error) {
	var chunks []storage.Chunk

	err := filepath.WalkDir(idx.Root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		// Skip hidden dirs and known non-source dirs
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || skipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(d.Name()))
		if !IndexableExtensions[ext] {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}

		rel, _ := filepath.Rel(idx.Root, path)
		textChunks := FileChunk(rel, string(data), 80, 10)

		for _, c := range textChunks {
			chunks = append(chunks, storage.Chunk{
				ProjectID: idx.ProjectID,
				FilePath:  rel,
				ChunkType: "generic",
				Content:   c,
			})
		}
		return nil
	})

	return chunks, err
}
