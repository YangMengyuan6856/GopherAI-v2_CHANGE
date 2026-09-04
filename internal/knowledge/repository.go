package knowledge

import (
	"GopherAI/model"
	"context"
	"errors"

	"gorm.io/gorm"
)

type Repository interface {
	FindByContentHash(ctx context.Context, tenantID string, contentHash string) (*model.KnowledgeDocument, *model.KnowledgeJob, error)
	CreateUpload(ctx context.Context, document *model.KnowledgeDocument, version *model.KnowledgeDocumentVersion, job *model.KnowledgeJob, event *model.OutboxEvent) error
	ListDocuments(ctx context.Context, tenantID string) ([]model.KnowledgeDocument, error)
	GetJob(ctx context.Context, tenantID string, jobID string) (*model.KnowledgeJob, error)
}

type GormRepository struct {
	db *gorm.DB
}

func NewGormRepository(db *gorm.DB) *GormRepository {
	return &GormRepository{db: db}
}

func (repository *GormRepository) FindByContentHash(ctx context.Context, tenantID string, contentHash string) (*model.KnowledgeDocument, *model.KnowledgeJob, error) {
	if repository == nil || repository.db == nil {
		return nil, nil, gorm.ErrInvalidDB
	}
	document := new(model.KnowledgeDocument)
	err := repository.db.WithContext(ctx).
		Where("tenant_id = ? AND content_hash = ? AND status <> ?", tenantID, contentHash, DocumentStatusDeleted).
		Order("created_at DESC").First(document).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	job := new(model.KnowledgeJob)
	err = repository.db.WithContext(ctx).
		Where("tenant_id = ? AND document_id = ?", tenantID, document.ID).
		Order("created_at DESC").First(job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return document, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return document, job, nil
}

func (repository *GormRepository) CreateUpload(ctx context.Context, document *model.KnowledgeDocument, version *model.KnowledgeDocumentVersion, job *model.KnowledgeJob, event *model.OutboxEvent) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	return repository.db.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		for _, value := range []any{document, version, job, event} {
			if err := transaction.Create(value).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (repository *GormRepository) ListDocuments(ctx context.Context, tenantID string) ([]model.KnowledgeDocument, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	var documents []model.KnowledgeDocument
	err := repository.db.WithContext(ctx).
		Where("tenant_id = ? AND status <> ?", tenantID, DocumentStatusDeleted).
		Order("created_at DESC").Find(&documents).Error
	return documents, err
}

func (repository *GormRepository) GetJob(ctx context.Context, tenantID string, jobID string) (*model.KnowledgeJob, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	job := new(model.KnowledgeJob)
	err := repository.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, jobID).First(job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return job, err
}
