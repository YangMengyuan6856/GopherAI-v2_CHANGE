package knowledge

import (
	"testing"

	"GopherAI/model"
)

func TestAnalyzeRevisionDiffsByStructuredIdentityAndContentHash(t *testing.T) {
	previous := []model.KnowledgeChunk{
		{ID: "old-config", Ordinal: 0, SectionPath: "Service > Retry", ContentHash: "same", EmbeddingVersion: "embedding-v1"},
		{ID: "old-health", Ordinal: 1, SectionPath: "Service > Health", ContentHash: "old", EmbeddingVersion: "embedding-v1"},
		{ID: "old-removed", Ordinal: 2, SectionPath: "Service > Removed", ContentHash: "removed", EmbeddingVersion: "embedding-v1"},
	}
	current := []model.KnowledgeChunk{
		{ID: "new-config", Ordinal: 0, SectionPath: "Service > Retry", ContentHash: "same", EmbeddingVersion: "embedding-v1"},
		{ID: "new-health", Ordinal: 1, SectionPath: "Service > Health", ContentHash: "new", EmbeddingVersion: "embedding-v1"},
		{ID: "new-added", Ordinal: 2, SectionPath: "Service > Queue", ContentHash: "added", EmbeddingVersion: "embedding-v1"},
	}
	assignLogicalChunkKeys(ParserVersionDataV1, current)

	stats := analyzeRevision(previous, current, ParserVersionDataV1)

	if stats.Version != RevisionIndexStatsVersion || stats.ChunkCount != 3 || stats.UnchangedChunks != 1 || stats.ModifiedChunks != 1 || stats.AddedChunks != 1 || stats.DeletedChunks != 1 {
		t.Fatalf("unexpected revision diff: %+v", stats)
	}
	if current[0].EmbeddingSourceChunkID != "old-config" || current[1].EmbeddingSourceChunkID != "" {
		t.Fatalf("only byte-identical compatible chunks may reuse the previous vector: %+v", current)
	}
}

func TestLogicalChunkIdentitySurvivesLineAndOrdinalShift(t *testing.T) {
	before := []model.KnowledgeChunk{{Ordinal: 1, SectionPath: " package worker > method Service.Run "}}
	after := []model.KnowledgeChunk{{Ordinal: 8, SectionPath: "package   worker > method Service.Run"}}
	assignLogicalChunkKeys(ParserVersionGoV1, before)
	assignLogicalChunkKeys(ParserVersionGoV1, after)
	if before[0].LogicalKey != after[0].LogicalKey {
		t.Fatalf("symbol identity must not depend on line or global ordinal: before=%s after=%s", before[0].LogicalKey, after[0].LogicalKey)
	}
}

func TestEmbeddingModelChangeForcesRecomputation(t *testing.T) {
	previous := []model.KnowledgeChunk{{ID: "old", Ordinal: 0, SectionPath: "Config", ContentHash: "same", EmbeddingVersion: "embedding-v1"}}
	current := []model.KnowledgeChunk{{ID: "new", Ordinal: 0, SectionPath: "Config", ContentHash: "same", EmbeddingVersion: "embedding-v2"}}
	assignLogicalChunkKeys(ParserVersionV1, current)
	stats := analyzeRevision(previous, current, ParserVersionV1)
	if stats.UnchangedChunks != 1 || current[0].EmbeddingSourceChunkID != "" {
		t.Fatalf("content may be unchanged but an incompatible vector must not be reused: stats=%+v chunk=%+v", stats, current[0])
	}
}
