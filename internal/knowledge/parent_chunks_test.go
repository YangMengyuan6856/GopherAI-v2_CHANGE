package knowledge

import (
	"strings"
	"testing"
	"time"

	"GopherAI/model"
)

func TestAttachParentChunksGroupsStructuredChildrenAndKeepsExactLinks(t *testing.T) {
	now := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	document := model.KnowledgeDocument{ID: "document-1", TenantID: "tenant-a", UserID: "user-a"}
	version := model.KnowledgeDocumentVersion{
		Version: 2, ParserVersion: ParserVersionDataV1, SourceKind: SourceKindUpload,
		SourceRevision: "revision-2", Authority: AuthorityUserUpload, EffectiveAt: &now,
	}
	children := []model.KnowledgeChunk{
		{ID: "retry", SectionPath: "service > retry > max_attempts", LineStart: 1, LineEnd: 2, Content: "max_attempts: 6", TokenCount: 4},
		{ID: "dlx", SectionPath: "service > retry > dead_letter_exchange", LineStart: 3, LineEnd: 4, Content: "dead_letter_exchange: jobs.dlx", TokenCount: 5},
		{ID: "port", SectionPath: "server > port", LineStart: 8, LineEnd: 8, Content: "port: 8081", TokenCount: 3},
	}

	parents := attachParentChunks(document, version, children, now)

	if len(parents) != 2 || children[0].ParentChunkID == "" || children[0].ParentChunkID != children[1].ParentChunkID || children[2].ParentChunkID == children[0].ParentChunkID {
		t.Fatalf("unexpected parent grouping: parents=%+v children=%+v", parents, children)
	}
	if parents[0].ChunkKind != ChunkKindParent || children[0].ChunkKind != ChunkKindChild || parents[0].SectionPath != "service > retry" {
		t.Fatalf("parent/child kind or scope is invalid: parent=%+v child=%+v", parents[0], children[0])
	}
	if !strings.Contains(parents[0].Content, "max_attempts: 6") || !strings.Contains(parents[0].Content, "dead_letter_exchange: jobs.dlx") || parents[0].LineStart != 1 || parents[0].LineEnd != 4 {
		t.Fatalf("parent context lost sibling evidence: %+v", parents[0])
	}
	if parents[0].SourceRevision != "revision-2" || parents[0].Authority != AuthorityUserUpload || parents[0].LogicalKey == "" {
		t.Fatalf("parent lost source lineage: %+v", parents[0])
	}
}

func TestAttachParentChunksSplitsOversizedScope(t *testing.T) {
	document := model.KnowledgeDocument{ID: "document-1", TenantID: "tenant-a", UserID: "user-a"}
	version := model.KnowledgeDocumentVersion{Version: 1, ParserVersion: ParserVersionGoV1}
	children := []model.KnowledgeChunk{
		{ID: "a", SectionPath: "package worker > function A", Content: "A", TokenCount: 800},
		{ID: "b", SectionPath: "package worker > function B", Content: "B", TokenCount: 800},
	}
	parents := attachParentChunks(document, version, children, time.Now().UTC())
	if len(parents) != 2 || children[0].ParentChunkID == children[1].ParentChunkID {
		t.Fatalf("oversized parent scope must be deterministically bounded: parents=%+v children=%+v", parents, children)
	}
}
