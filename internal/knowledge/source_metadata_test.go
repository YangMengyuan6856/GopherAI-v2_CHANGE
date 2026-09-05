package knowledge

import (
	"testing"
	"time"

	"GopherAI/model"
)

func TestInitializeUploadSourceBuildsAuditableLineage(t *testing.T) {
	effectiveAt := time.Date(2026, 9, 5, 1, 2, 3, 0, time.FixedZone("CST", 8*60*60))
	version := new(model.KnowledgeDocumentVersion)

	initializeUploadSource(version, " revision-42 ", effectiveAt, 3)

	if version.SourceKind != SourceKindUpload || version.SourceRevision != "revision-42" || version.Authority != AuthorityUserUpload {
		t.Fatalf("unexpected source identity: %+v", version)
	}
	if version.EffectiveAt == nil || !version.EffectiveAt.Equal(effectiveAt.UTC()) || version.SupersedesVersion != 3 {
		t.Fatalf("unexpected source validity or lineage: %+v", version)
	}
}

func TestNormalizeLegacyVersionKeepsHistoricalRowsSearchable(t *testing.T) {
	fallback := time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)
	legacy := model.KnowledgeDocumentVersion{ContentHash: "legacy-hash"}

	normalized := normalizeVersionSource(legacy, fallback)

	if normalized.SourceKind != SourceKindUpload || normalized.SourceRevision != "legacy-hash" || normalized.Authority != AuthorityLegacy {
		t.Fatalf("legacy source was not normalized safely: %+v", normalized)
	}
	if normalized.EffectiveAt == nil || !normalized.EffectiveAt.Equal(fallback) {
		t.Fatalf("legacy source did not receive deterministic effective time: %+v", normalized)
	}
}

func TestApplyVersionSourceToChunkCopiesPointers(t *testing.T) {
	effectiveAt := time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)
	originalEffectiveAt := effectiveAt
	expiredAt := effectiveAt.Add(time.Hour)
	version := model.KnowledgeDocumentVersion{
		SourceKind: SourceKindRepository, SourceRevision: "abc123", Authority: AuthorityRepository,
		EffectiveAt: &effectiveAt, ExpiredAt: &expiredAt, SupersedesVersion: 5,
	}
	chunk := new(model.KnowledgeChunk)

	applyVersionSourceToChunk(chunk, version)
	*version.EffectiveAt = version.EffectiveAt.Add(time.Hour)

	if chunk.SourceKind != SourceKindRepository || chunk.SourceRevision != "abc123" || chunk.Authority != AuthorityRepository || chunk.SupersedesVersion != 5 {
		t.Fatalf("chunk lost source metadata: %+v", chunk)
	}
	if chunk.EffectiveAt == nil || !chunk.EffectiveAt.Equal(originalEffectiveAt) || chunk.ExpiredAt == nil || !chunk.ExpiredAt.Equal(expiredAt) {
		t.Fatalf("chunk source validity aliases mutable version pointers: %+v", chunk)
	}
}
