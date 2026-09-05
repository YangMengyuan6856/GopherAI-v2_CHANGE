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
		Select("chunks.id, chunks.document_id, chunks.document_version, chunks.tenant_id, chunks.user_id, documents.display_name, chunks.section_path, chunks.line_start, chunks.line_end, chunks.content, chunks.content_hash, chunks.parent_chunk_id, chunks.source_kind, chunks.source_revision, chunks.authority, chunks.effective_at, chunks.expired_at, chunks.supersedes_version").
		Joins("JOIN knowledge_documents AS documents ON documents.id = chunks.document_id").
		Where("chunks.id IN ? AND chunks.tenant_id = ? AND chunks.user_id = ? AND chunks.index_status = ?", chunkIDs, tenantID, userID, "indexed").
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
			Authority: row.Authority, EffectiveAt: row.EffectiveAt, ExpiredAt: row.ExpiredAt, SupersedesVersion: row.SupersedesVersion,
		}
	}
	return result, nil
}
