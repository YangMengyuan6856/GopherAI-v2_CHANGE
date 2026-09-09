package knowledge

import (
	"GopherAI/model"
	"context"
	"errors"
	"testing"
)

type rehydrationRepository struct {
	chunks []model.KnowledgeChunk
	err    error
}

func (repository rehydrationRepository) ListActiveIndexedChunks(context.Context) ([]model.KnowledgeChunk, error) {
	return append([]model.KnowledgeChunk(nil), repository.chunks...), repository.err
}

type rehydrationIndexer struct {
	counts    []int
	countCall int
	indexCall int
	stats     VectorIndexStats
	indexErr  error
	pruneCall int
	pruned    int
	pruneErr  error
}

func (indexer *rehydrationIndexer) PresentChunkCount(context.Context, []model.KnowledgeChunk) (int, error) {
	if indexer.countCall >= len(indexer.counts) {
		return 0, errors.New("unexpected count call")
	}
	value := indexer.counts[indexer.countCall]
	indexer.countCall++
	return value, nil
}

func (indexer *rehydrationIndexer) PruneStaleChunks(context.Context, []model.KnowledgeChunk) (int, error) {
	indexer.pruneCall++
	return indexer.pruned, indexer.pruneErr
}

func (indexer *rehydrationIndexer) IndexIncremental(context.Context, []model.KnowledgeChunk) (VectorIndexStats, error) {
	indexer.indexCall++
	return indexer.stats, indexer.indexErr
}

func TestRehydrateActiveProjectionSkipsCompleteProjection(t *testing.T) {
	chunks := rehydrationChunks()
	indexer := &rehydrationIndexer{counts: []int{len(chunks)}, pruned: 3}
	result, err := RehydrateActiveProjection(context.Background(), rehydrationRepository{chunks: chunks}, indexer)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rebuilt || result.PrunedChunks != 3 || result.PresentAfter != len(chunks) || indexer.indexCall != 0 || indexer.pruneCall != 1 {
		t.Fatalf("unexpected complete projection result: %+v calls=%d", result, indexer.indexCall)
	}
}

func TestRehydrateActiveProjectionFailsClosedWhenPruningFails(t *testing.T) {
	indexer := &rehydrationIndexer{pruneErr: errors.New("scan failed")}
	result, err := RehydrateActiveProjection(context.Background(), rehydrationRepository{chunks: rehydrationChunks()}, indexer)
	if err == nil || result.PresentBefore != 0 || indexer.indexCall != 0 {
		t.Fatalf("expected pruning failure before projection verification: result=%+v err=%v", result, err)
	}
}

func TestRehydrateActiveProjectionRepairsAndVerifiesMissingProjection(t *testing.T) {
	chunks := rehydrationChunks()
	indexer := &rehydrationIndexer{counts: []int{0, len(chunks)}, stats: VectorIndexStats{EmbeddedChunks: 1, ReusedVectors: 1}}
	result, err := RehydrateActiveProjection(context.Background(), rehydrationRepository{chunks: chunks}, indexer)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Rebuilt || result.PresentBefore != 0 || result.PresentAfter != 2 || result.EmbeddedChunks != 1 || result.ReusedVectors != 1 || indexer.indexCall != 1 {
		t.Fatalf("unexpected repaired projection result: %+v calls=%d", result, indexer.indexCall)
	}
}

func TestRehydrateActiveProjectionFailsClosedWhenVerificationIsIncomplete(t *testing.T) {
	chunks := rehydrationChunks()
	indexer := &rehydrationIndexer{counts: []int{0, 1}}
	result, err := RehydrateActiveProjection(context.Background(), rehydrationRepository{chunks: chunks}, indexer)
	if err == nil || !result.Rebuilt || result.PresentAfter != 1 {
		t.Fatalf("expected incomplete verification failure, result=%+v err=%v", result, err)
	}
}

func TestRehydrateActiveProjectionRejectsDuplicateOrParentRows(t *testing.T) {
	chunks := rehydrationChunks()
	chunks[1].ID = chunks[0].ID
	if _, err := RehydrateActiveProjection(context.Background(), rehydrationRepository{chunks: chunks}, &rehydrationIndexer{}); err == nil {
		t.Fatal("duplicate authoritative chunk must fail")
	}
	chunks = rehydrationChunks()
	chunks[1].ChunkKind = ChunkKindParent
	if _, err := RehydrateActiveProjection(context.Background(), rehydrationRepository{chunks: chunks}, &rehydrationIndexer{}); err == nil {
		t.Fatal("parent chunk must not enter vector projection")
	}
}

func rehydrationChunks() []model.KnowledgeChunk {
	return []model.KnowledgeChunk{
		{ID: "chunk-1", TenantID: "tenant-a", UserID: "user-a", DocumentID: "document-a", DocumentVersion: 1, ChunkKind: ChunkKindChild, IndexStatus: ChunkIndexStatusIndexed},
		{ID: "chunk-2", TenantID: "tenant-a", UserID: "user-a", DocumentID: "document-a", DocumentVersion: 1, ChunkKind: ChunkKindChild, IndexStatus: ChunkIndexStatusIndexed},
	}
}
