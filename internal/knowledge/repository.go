package knowledge

import (
	"GopherAI/model"
	"context"
	"encoding/json"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository interface {
	FindByContentHash(ctx context.Context, tenantID string, contentHash string) (*model.KnowledgeDocument, *model.KnowledgeJob, error)
	FindDocument(ctx context.Context, tenantID string, userID string, documentID string) (*model.KnowledgeDocument, error)
	FindVersionByContentHash(ctx context.Context, tenantID string, documentID string, contentHash string) (*model.KnowledgeDocumentVersion, *model.KnowledgeJob, error)
	CreateUpload(ctx context.Context, document *model.KnowledgeDocument, version *model.KnowledgeDocumentVersion, job *model.KnowledgeJob, event *model.OutboxEvent) error
	CreateVersionUpload(ctx context.Context, tenantID string, userID string, version *model.KnowledgeDocumentVersion, job *model.KnowledgeJob, event *model.OutboxEvent) (*model.KnowledgeDocument, error)
	ListDocuments(ctx context.Context, tenantID string) ([]model.KnowledgeDocument, error)
	GetJob(ctx context.Context, tenantID string, jobID string) (*model.KnowledgeJob, error)
}

func (repository *GormRepository) FindDocument(ctx context.Context, tenantID string, userID string, documentID string) (*model.KnowledgeDocument, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	document := new(model.KnowledgeDocument)
	err := repository.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ? AND user_id = ? AND status <> ?", documentID, tenantID, userID, DocumentStatusDeleted).
		First(document).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return document, err
}

func (repository *GormRepository) FindVersionByContentHash(ctx context.Context, tenantID string, documentID string, contentHash string) (*model.KnowledgeDocumentVersion, *model.KnowledgeJob, error) {
	if repository == nil || repository.db == nil {
		return nil, nil, gorm.ErrInvalidDB
	}
	version := new(model.KnowledgeDocumentVersion)
	err := repository.db.WithContext(ctx).
		Table("knowledge_document_versions AS versions").
		Select("versions.*").
		Joins("JOIN knowledge_documents AS documents ON documents.id = versions.document_id").
		Where("documents.tenant_id = ? AND documents.id = ? AND versions.content_hash = ? AND documents.status <> ?", tenantID, documentID, contentHash, DocumentStatusDeleted).
		Order("versions.version DESC").First(version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	job := new(model.KnowledgeJob)
	err = repository.db.WithContext(ctx).
		Where("tenant_id = ? AND document_id = ? AND version = ?", tenantID, documentID, version.Version).
		Order("created_at DESC").First(job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return version, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return version, job, nil
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

func (repository *GormRepository) CreateVersionUpload(ctx context.Context, tenantID string, userID string, version *model.KnowledgeDocumentVersion, job *model.KnowledgeJob, event *model.OutboxEvent) (*model.KnowledgeDocument, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	document := new(model.KnowledgeDocument)
	err := repository.db.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND user_id = ? AND status <> ?", version.DocumentID, tenantID, userID, DocumentStatusDeleted).
			First(document).Error; err != nil {
			return err
		}
		latest := new(model.KnowledgeDocumentVersion)
		nextVersion := 1
		err := transaction.Where("document_id = ?", document.ID).Order("version DESC").First(latest).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			nextVersion = latest.Version + 1
		}
		version.Version = nextVersion
		job.TenantID = tenantID
		job.DocumentID = document.ID
		job.Version = nextVersion
		event.TenantID = tenantID
		event.AggregateID = document.ID
		event.AggregateVersion = nextVersion
		payload, marshalErr := json.Marshal(map[string]any{"job_id": job.ID, "document_id": document.ID, "version": nextVersion})
		if marshalErr != nil {
			return marshalErr
		}
		event.PayloadJSON = string(payload)
		for _, value := range []any{version, job, event} {
			if err := transaction.Create(value).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return document, err
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
