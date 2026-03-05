// Package django provides a Django-aware indexer that categorises Python
// source files by their role in a Django project (model, view, url, etc.).
package django

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/smkbarbosa/context-ia-manager/internal/indexer"
	"github.com/smkbarbosa/context-ia-manager/internal/storage"
)

// chunkType maps a Django filename convention to a semantic type label.
// Matching is done on the base filename (e.g. "models.py", "views.py").
var filenameTypes = map[string]string{
	"models.py":      "model",
	"views.py":       "view",
	"viewsets.py":    "view",
	"urls.py":        "url",
	"serializers.py": "serializer",
	"forms.py":       "form",
	"admin.py":       "admin",
	"signals.py":     "signal",
	"managers.py":    "manager",
	"tasks.py":       "task",
	"apps.py":        "app_config",
	"settings.py":    "settings",
	"conftest.py":    "test",
}

// contentHints are partial strings checked in file content to infer type
// when the filename alone doesn't match.
var contentHints = []struct {
	keyword   string
	chunkType string
}{
	{"from django.db import models", "model"},
	{"class Meta:", "model"},
	{"ModelSerializer", "serializer"},
	{"APIView", "view"},
	{"ViewSet", "view"},
	{"path(", "url"},
	{"urlpatterns", "url"},
	{"@shared_task", "task"},
	{"@receiver", "signal"},
	{"TestCase", "test"},
	{"def test_", "test"},
}

// skipDirs extended for Django
var skipDirs = map[string]bool{
	"migrations":    true, // indexed separately in compressed form
	"__pycache__":   true,
	".git":          true,
	".venv":         true,
	"venv":          true,
	"node_modules":  true,
	".pytest_cache": true,
	"htmlcov":       true,
	"staticfiles":   true,
	"media":         true,
}

// Indexer is a Django-aware project indexer.
type Indexer struct {
	Root      string
	ProjectID string
}

// New creates a Django indexer.
func New(root, projectID string) *Indexer {
	return &Indexer{Root: root, ProjectID: projectID}
}

// Index walks the Django project and returns typed chunks.
func (idx *Indexer) Index() ([]storage.Chunk, error) {
	var chunks []storage.Chunk

	err := filepath.WalkDir(idx.Root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || skipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}

		// Only Python files for Django-specific handling
		if strings.ToLower(filepath.Ext(d.Name())) != ".py" {
			// Still index non-Python files generically
			if indexer.IndexableExtensions[strings.ToLower(filepath.Ext(d.Name()))] {
				data, err := os.ReadFile(path)
				if err != nil {
					return nil
				}
				rel, _ := filepath.Rel(idx.Root, path)
				for _, c := range indexer.FileChunk(rel, string(data), 80, 10) {
					chunks = append(chunks, storage.Chunk{
						ProjectID: idx.ProjectID,
						FilePath:  rel,
						ChunkType: "generic",
						Content:   c,
					})
				}
			}
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		rel, _ := filepath.Rel(idx.Root, path)
		content := string(data)
		chunkType := inferType(d.Name(), content)

		for _, c := range indexer.FileChunk(rel, content, 80, 10) {
			chunks = append(chunks, storage.Chunk{
				ProjectID: idx.ProjectID,
				FilePath:  rel,
				ChunkType: chunkType,
				Content:   c,
			})
		}
		return nil
	})

	return chunks, err
}

// AppMap returns a structural overview of apps, models, views, and urls
// found in the Django project. Used by the ciam_django_map MCP tool.
func (idx *Indexer) AppMap() (map[string]AppInfo, error) {
	apps := map[string]AppInfo{}

	_ = filepath.WalkDir(idx.Root, func(path string, d os.DirEntry, _ error) error {
		if !d.IsDir() {
			return nil
		}
		// A directory with an apps.py is a Django app
		appsFile := filepath.Join(path, "apps.py")
		if _, err := os.Stat(appsFile); err != nil {
			return nil
		}

		rel, _ := filepath.Rel(idx.Root, path)
		info := AppInfo{Name: rel}

		summarise := func(filename, field string) string {
			content, err := os.ReadFile(filepath.Join(path, filename))
			if err != nil {
				return ""
			}
			_ = field
			return firstN(string(content), 400)
		}

		info.ModelsSummary = summarise("models.py", "models")
		info.ViewsSummary = summarise("views.py", "views")
		info.URLSSummary = summarise("urls.py", "urls")
		info.SerializersSummary = summarise("serializers.py", "serializers")

		apps[rel] = info
		return nil
	})

	return apps, nil
}

// AppInfo holds a structural summary of a single Django app.
type AppInfo struct {
	Name               string
	ModelsSummary      string
	ViewsSummary       string
	URLSSummary        string
	SerializersSummary string
}

// inferType determines the semantic type of a Python file.
func inferType(filename, content string) string {
	// First try filename convention
	lname := strings.ToLower(filename)
	if t, ok := filenameTypes[lname]; ok {
		return t
	}

	// Fallback to content hints
	for _, hint := range contentHints {
		if strings.Contains(content, hint.keyword) {
			return hint.chunkType
		}
	}

	// test_*.py / *_test.py pattern
	if strings.HasPrefix(lname, "test_") || strings.HasSuffix(strings.TrimSuffix(lname, ".py"), "_test") {
		return "test"
	}

	return "generic"
}

// firstN returns the first n bytes of s.
func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + " [truncated]"
}
