package rag

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

type GormAuthorityRepository struct {
	db *gorm.DB
}

func NewGormAuthorityRepository(db *gorm.DB) *GormAuthorityRepository {
	return &GormAuthorityRepository{db: db}
}

func (repository *GormAuthorityRepository) FindAccessibleChunks(ctx context.Context, tenantID string, userID string, chunkIDs []string) (map[string]ChunkAuthority, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	if len(chunkIDs) == 0 {
		return map[string]ChunkAuthority{}, nil
	}
	type chunkRow struct {
		ID                string
		DocumentID        string
		DocumentVersion   int
		TenantID          string
		UserID            string
		DisplayName       string
		SectionPath       string
		LineStart         int
		LineEnd           int
		Content           string
		ContentHash       string
		ParentChunkID     string
		ParentContext     string
		ParentSection     string
		ParentLineStart   int
		ParentLineEnd     int
		SourceKind        string
		SourceRevision    string
		Authority         int
		EffectiveAt       *time.Time
		ExpiredAt         *time.Time
		SupersedesVersion int
	}
	var rows []chunkRow
	now := time.Now().UTC()
	err := repository.db.WithContext(ctx).
		Table("knowledge_chunks AS chunks").
		Select("chunks.id, chunks.document_id, chunks.document_version, chunks.tenant_id, chunks.user_id, documents.display_name, chunks.section_path, chunks.line_start, chunks.line_end, chunks.content, chunks.content_hash, chunks.parent_chunk_id, parents.content AS parent_context, parents.section_path AS parent_section, parents.line_start AS parent_line_start, parents.line_end AS parent_line_end, chunks.source_kind, chunks.source_revision, chunks.authority, chunks.effective_at, chunks.expired_at, chunks.supersedes_version").
		Joins("JOIN knowledge_documents AS documents ON documents.id = chunks.document_id").
		Joins("LEFT JOIN knowledge_chunks AS parents ON parents.id = chunks.parent_chunk_id AND parents.document_id = chunks.document_id AND parents.document_version = chunks.document_version AND parents.tenant_id = chunks.tenant_id AND parents.user_id = chunks.user_id AND parents.index_status = ? AND parents.chunk_kind = ? AND (parents.effective_at IS NULL OR parents.effective_at <= ?) AND (parents.expired_at IS NULL OR parents.expired_at > ?)", "indexed", "parent", now, now).
		Where("chunks.id IN ? AND chunks.tenant_id = ? AND chunks.user_id = ? AND chunks.index_status = ?", chunkIDs, tenantID, userID, "indexed").
		Where("(chunks.chunk_kind = ? OR chunks.chunk_kind = '' OR chunks.chunk_kind IS NULL)", "child").
		Where("documents.tenant_id = ? AND documents.user_id = ? AND documents.status = ?", tenantID, userID, "indexed").
		Where("chunks.document_version = documents.current_version").
		Where("(chunks.effective_at IS NULL OR chunks.effective_at <= ?) AND (chunks.expired_at IS NULL OR chunks.expired_at > ?)", now, now).
		Scan(&rows).Error
	if err != nil {
		return nil, errors.Join(errors.New("query accessible knowledge chunks"), err)
	}
	result := make(map[string]ChunkAuthority, len(rows))
	for _, row := range rows {
		result[row.ID] = ChunkAuthority{
			ID: row.ID, DocumentID: row.DocumentID, DocumentVersion: row.DocumentVersion,
			TenantID: row.TenantID, UserID: row.UserID, DisplayName: row.DisplayName,
			SectionPath: row.SectionPath, LineStart: row.LineStart, LineEnd: row.LineEnd,
			Content: row.Content, ContentHash: row.ContentHash,
			ParentChunkID: row.ParentChunkID, SourceKind: row.SourceKind, SourceRevision: row.SourceRevision,
			ParentContext: row.ParentContext, ParentSection: row.ParentSection,
			ParentLineStart: row.ParentLineStart, ParentLineEnd: row.ParentLineEnd,
			Authority: row.Authority, EffectiveAt: row.EffectiveAt, ExpiredAt: row.ExpiredAt, SupersedesVersion: row.SupersedesVersion,
		}
	}
	return result, nil
}
