package memory

import (
	"github.com/smkbarbosa/context-ia-manager/internal/embeddings"
	"github.com/smkbarbosa/context-ia-manager/internal/storage"
)

// Service handles persistent memory across sessions.
type Service struct {
	db      *storage.DB
	embedder *embeddings.Client
}

// New creates a memory service.
func New(db *storage.DB, embedder *embeddings.Client) *Service {
	return &Service{db: db, embedder: embedder}
}

// Remember stores content with its embedding.
func (s *Service) Remember(memType, content string) error {
	vec, err := s.embedder.Embed(content)
	if err != nil {
		return err
	}
	return s.db.StoreMemory(memType, content, vec)
}

// Recall retrieves the top memories semantically relevant to query.
func (s *Service) Recall(query string, limit int) ([]storage.Memory, error) {
	vec, err := s.embedder.Embed(query)
	if err != nil {
		return nil, err
	}
	return s.db.RecallMemories(vec, limit)
}
