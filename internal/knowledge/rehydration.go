package knowledge

import (
	"GopherAI/model"
	"context"
	"errors"
	"fmt"
	"strings"
)

const ProjectionRehydrationVersion = "knowledge-projection-rehydration-v1"

type ActiveIndexedChunkRepository interface {
	ListActiveIndexedChunks(ctx context.Context) ([]model.KnowledgeChunk, error)
}

type ProjectionChunkIndexer interface {
	PresentChunkCount(ctx context.Context, chunks []model.KnowledgeChunk) (int, error)
	IndexIncremental(ctx context.Context, chunks []model.KnowledgeChunk) (VectorIndexStats, error)
}

type ProjectionRehydrationResult struct {
	Version        string `json:"version"`
	ExpectedChunks int    `json:"expected_chunks"`
	PresentBefore  int    `json:"present_before"`
	PresentAfter   int    `json:"present_after"`
	Rebuilt        bool   `json:"rebuilt"`
	EmbeddedChunks int    `json:"embedded_chunks"`
	ReusedVectors  int    `json:"reused_vectors"`
}

// RehydrateActiveProjection repairs Redis from the authoritative MySQL
// projection before the worker advertises readiness. A partial rebuild is
// verified a second time and fails closed instead of serving an empty RAG
// index while documents still claim to be indexed.
func RehydrateActiveProjection(ctx context.Context, repository ActiveIndexedChunkRepository, indexer ProjectionChunkIndexer) (ProjectionRehydrationResult, error) {
	result := ProjectionRehydrationResult{Version: ProjectionRehydrationVersion}
	if repository == nil || indexer == nil {
		return result, errors.New("active chunk repository and projection indexer are required")
	}
	chunks, err := repository.ListActiveIndexedChunks(ctx)
	if err != nil {
		return result, fmt.Errorf("list authoritative chunks: %w", err)
	}
	result.ExpectedChunks = len(chunks)
	if err := validateRehydrationChunks(chunks); err != nil {
		return result, err
	}
	if len(chunks) == 0 {
		return result, nil
	}
	present, err := indexer.PresentChunkCount(ctx, chunks)
	if err != nil {
		return result, err
	}
	result.PresentBefore = present
	if present == len(chunks) {
		result.PresentAfter = present
		return result, nil
	}
	if present < 0 || present > len(chunks) {
		return result, fmt.Errorf("redis projection count %d exceeds authoritative count %d", present, len(chunks))
	}
	stats, err := indexer.IndexIncremental(ctx, chunks)
	if err != nil {
		return result, fmt.Errorf("rebuild redis projection: %w", err)
	}
	result.Rebuilt = true
	result.EmbeddedChunks = stats.EmbeddedChunks
	result.ReusedVectors = stats.ReusedVectors
	present, err = indexer.PresentChunkCount(ctx, chunks)
	if err != nil {
		return result, err
	}
	result.PresentAfter = present
	if present != len(chunks) {
		return result, fmt.Errorf("redis projection verification failed: got %d want %d", present, len(chunks))
	}
	return result, nil
}

func validateRehydrationChunks(chunks []model.KnowledgeChunk) error {
	seen := make(map[string]struct{}, len(chunks))
	for _, chunk := range chunks {
		id := strings.TrimSpace(chunk.ID)
		if id == "" || strings.TrimSpace(chunk.TenantID) == "" || strings.TrimSpace(chunk.UserID) == "" || strings.TrimSpace(chunk.DocumentID) == "" || chunk.DocumentVersion < 1 || chunk.IndexStatus != ChunkIndexStatusIndexed {
			return fmt.Errorf("authoritative chunk %q is invalid", id)
		}
		if chunk.ChunkKind != "" && chunk.ChunkKind != ChunkKindChild {
			return fmt.Errorf("authoritative chunk %s is not a child", id)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("authoritative chunk %s is duplicated", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}
