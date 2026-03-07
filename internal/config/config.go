package config

import (
	"os"
	"path/filepath"
)

// Config holds all runtime configuration for ciam.
type Config struct {
	APIURL      string
	OllamaURL   string
	OllamaModel string
	CodeModel   string // optional: model for ciam_draft generation (empty = disabled)
	DBPath      string
	ProjectPath string
}

// Load reads config from environment variables with sensible defaults.
func Load() *Config {
	home, _ := os.UserHomeDir()
	defaultDB := filepath.Join(home, ".local", "share", "ciam", "ciam.db")

	return &Config{
		APIURL:      getenv("CIAM_API_URL", "http://localhost:8080"),
		OllamaURL:   getenv("CIAM_OLLAMA_URL", "http://localhost:11434"),
		OllamaModel: getenv("CIAM_OLLAMA_MODEL", "nomic-embed-text"),
		CodeModel:   getenv("CIAM_CODE_MODEL", ""), // empty = ciam_draft disabled
		DBPath:      getenv("CIAM_DB_PATH", defaultDB),
		ProjectPath: getenv("CIAM_PROJECT_PATH", "."),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
