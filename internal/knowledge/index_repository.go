package knowledge

import (
	"GopherAI/model"
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type IndexWork struct {
	Document model.KnowledgeDocument
	Version  model.KnowledgeDocumentVersion
	Job      model.KnowledgeJob
	Complete bool
}

type IndexRepository interface {
	ClaimIndexJob(ctx context.Context, tenantID string, jobID string, documentID string, version int) (IndexWork, error)
	ReplaceChunks(ctx context.Context, documentID string, version int, chunks []model.KnowledgeChunk) error
	CompleteIndex(ctx context.Context, work IndexWork, embeddingVersion string, completedAt time.Time) error
	RecordIndexRetry(ctx context.Context, work IndexWork, code string) error
	FailIndex(ctx context.Context, work IndexWork, code string) error
	FailIndexByIdentity(ctx context.Context, tenantID string, jobID string, documentID string, version int, code string) error
}

func (repository *GormRepository) ClaimIndexJob(ctx context.Context, tenantID string, jobID string, documentID string, version int) (IndexWork, error) {
	if repository == nil || repository.db == nil {
		return IndexWork{}, gorm.ErrInvalidDB
	}
	work := IndexWork{}
	err := repository.db.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND document_id = ? AND version = ? AND job_type = ?", jobID, tenantID, documentID, version, JobTypeDocumentIndex).
			First(&work.Job).Error; err != nil {
			return err
		}
		if work.Job.Status == JobStatusCompleted {
			work.Complete = true
			return nil
		}
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND status <> ?", documentID, tenantID, DocumentStatusDeleted).
			First(&work.Document).Error; err != nil {
			return err
		}
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("document_id = ? AND version = ?", documentID, version).
			First(&work.Version).Error; err != nil {
			return err
		}
		work.Job.Status = JobStatusProcessing
		work.Job.Attempt++
		work.Job.LastErrorCode = ""
		if err := transaction.Save(&work.Job).Error; err != nil {
			return err
		}
		if err := transaction.Model(&model.KnowledgeDocument{}).Where("id = ? AND tenant_id = ?", documentID, tenantID).
			Updates(map[string]any{"status": DocumentStatusParsing, "last_error_code": ""}).Error; err != nil {
			return err
		}
		return transaction.Model(&model.KnowledgeDocumentVersion{}).Where("document_id = ? AND version = ?", documentID, version).
			Updates(map[string]any{"status": DocumentStatusParsing}).Error
	})
	return work, err
}

func (repository *GormRepository) ReplaceChunks(ctx context.Context, documentID string, version int, chunks []model.KnowledgeChunk) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	return repository.db.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if err := transaction.Where("document_id = ? AND document_version = ?", documentID, version).Delete(&model.KnowledgeChunk{}).Error; err != nil {
			return err
		}
		if len(chunks) == 0 {
			return nil
		}
		return transaction.CreateInBatches(chunks, 100).Error
	})
}

func (repository *GormRepository) CompleteIndex(ctx context.Context, work IndexWork, embeddingVersion string, completedAt time.Time) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	return repository.db.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if err := transaction.Model(&model.KnowledgeChunk{}).
			Where("document_id = ? AND document_version = ?", work.Document.ID, work.Version.Version).
			Updates(map[string]any{"index_status": ChunkIndexStatusIndexed, "embedding_version": embeddingVersion}).Error; err != nil {
			return err
		}
		if err := transaction.Model(&model.KnowledgeDocumentVersion{}).
			Where("document_id = ? AND version = ?", work.Document.ID, work.Version.Version).
			Updates(map[string]any{"status": DocumentStatusIndexed, "embedding_version": embeddingVersion, "indexed_at": completedAt}).Error; err != nil {
			return err
		}
		if err := transaction.Model(&model.KnowledgeDocument{}).Where("id = ? AND tenant_id = ?", work.Document.ID, work.Document.TenantID).
			Updates(map[string]any{"status": DocumentStatusIndexed, "last_error_code": ""}).Error; err != nil {
			return err
		}
		return transaction.Model(&model.KnowledgeJob{}).Where("id = ? AND tenant_id = ?", work.Job.ID, work.Job.TenantID).
			Updates(map[string]any{"status": JobStatusCompleted, "last_error_code": ""}).Error
	})
}

func (repository *GormRepository) RecordIndexRetry(ctx context.Context, work IndexWork, code string) error {
	return repository.recordIndexFailure(ctx, work, JobStatusRetrying, DocumentStatusParsing, code)
}

func (repository *GormRepository) FailIndex(ctx context.Context, work IndexWork, code string) error {
	return repository.recordIndexFailure(ctx, work, JobStatusFailed, DocumentStatusFailed, code)
}

func (repository *GormRepository) FailIndexByIdentity(ctx context.Context, tenantID string, jobID string, documentID string, version int, code string) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	return repository.db.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if err := transaction.Model(&model.KnowledgeJob{}).
			Where("id = ? AND tenant_id = ? AND document_id = ? AND version = ?", jobID, tenantID, documentID, version).
			Updates(map[string]any{"status": JobStatusFailed, "last_error_code": code}).Error; err != nil {
			return err
		}
		if err := transaction.Model(&model.KnowledgeDocument{}).Where("id = ? AND tenant_id = ?", documentID, tenantID).
			Updates(map[string]any{"status": DocumentStatusFailed, "last_error_code": code}).Error; err != nil {
			return err
		}
		return transaction.Model(&model.KnowledgeDocumentVersion{}).Where("document_id = ? AND version = ?", documentID, version).
			Updates(map[string]any{"status": DocumentStatusFailed}).Error
	})
}

func (repository *GormRepository) recordIndexFailure(ctx context.Context, work IndexWork, jobStatus string, documentStatus string, code string) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	return repository.db.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if err := transaction.Model(&model.KnowledgeJob{}).Where("id = ? AND tenant_id = ?", work.Job.ID, work.Job.TenantID).
			Updates(map[string]any{"status": jobStatus, "last_error_code": code}).Error; err != nil {
			return err
		}
		if err := transaction.Model(&model.KnowledgeDocument{}).Where("id = ? AND tenant_id = ?", work.Document.ID, work.Document.TenantID).
			Updates(map[string]any{"status": documentStatus, "last_error_code": code}).Error; err != nil {
			return err
		}
		return transaction.Model(&model.KnowledgeDocumentVersion{}).Where("document_id = ? AND version = ?", work.Document.ID, work.Version.Version).
			Updates(map[string]any{"status": documentStatus}).Error
	})
}

func isRecordNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
