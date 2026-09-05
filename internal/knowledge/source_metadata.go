package knowledge

import (
	"strings"
	"time"

	"GopherAI/model"
)

const (
	SourceKindUpload     = "upload"
	SourceKindRepository = "repository"

	AuthorityLegacy     = 40
	AuthorityUserUpload = 50
	AuthorityRepository = 80
)

func initializeUploadSource(version *model.KnowledgeDocumentVersion, revision string, effectiveAt time.Time, supersedesVersion int) {
	if version == nil {
		return
	}
	version.SourceKind = SourceKindUpload
	version.SourceRevision = strings.TrimSpace(revision)
	version.Authority = AuthorityUserUpload
	effective := effectiveAt.UTC()
	version.EffectiveAt = &effective
	version.SupersedesVersion = supersedesVersion
}

func normalizeVersionSource(version model.KnowledgeDocumentVersion, fallbackTime time.Time) model.KnowledgeDocumentVersion {
	if strings.TrimSpace(version.SourceKind) == "" {
		version.SourceKind = SourceKindUpload
	}
	if strings.TrimSpace(version.SourceRevision) == "" {
		version.SourceRevision = version.ContentHash
	}
	if version.Authority <= 0 {
		version.Authority = AuthorityLegacy
	}
	if version.EffectiveAt == nil {
		effective := fallbackTime.UTC()
		version.EffectiveAt = &effective
	}
	return version
}

func applyVersionSourceToChunk(chunk *model.KnowledgeChunk, version model.KnowledgeDocumentVersion) {
	if chunk == nil {
		return
	}
	chunk.SourceKind = version.SourceKind
	chunk.SourceRevision = version.SourceRevision
	chunk.Authority = version.Authority
	chunk.EffectiveAt = cloneTime(version.EffectiveAt)
	chunk.ExpiredAt = cloneTime(version.ExpiredAt)
	chunk.SupersedesVersion = version.SupersedesVersion
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyOfValue := value.UTC()
	return &copyOfValue
}
