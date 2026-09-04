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
	FindVersion(ctx context.Context, tenantID string, documentID string, version int) (*model.KnowledgeDocumentVersion, error)
	CreateUpload(ctx context.Context, document *model.KnowledgeDocument, version *model.KnowledgeDocumentVersion, job *model.KnowledgeJob, event *model.OutboxEvent) error
	CreateVersionUpload(ctx context.Context, tenantID string, userID string, version *model.KnowledgeDocumentVersion, job *model.KnowledgeJob, event *model.OutboxEvent) (*model.KnowledgeDocument, error)
	CreateDelete(ctx context.Context, tenantID string, userID string, documentID string, job *model.KnowledgeJob, event *model.OutboxEvent) (*model.KnowledgeDocument, *model.KnowledgeJob, bool, error)
	ListDocuments(ctx context.Context, tenantID string) ([]model.KnowledgeDocument, error)
	GetJob(ctx context.Context, tenantID string, jobID string) (*model.KnowledgeJob, error)
}

var ErrDocumentHasPendingIndex = errors.New("document has pending index work")

func (repository *GormRepository) FindVersion(ctx context.Context, tenantID string, documentID string, versionNumber int) (*model.KnowledgeDocumentVersion, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	version := new(model.KnowledgeDocumentVersion)
	err := repository.db.WithContext(ctx).
		Table("knowledge_document_versions AS versions").Select("versions.*").
		Joins("JOIN knowledge_documents AS documents ON documents.id = versions.document_id").
		Where("documents.tenant_id = ? AND documents.id = ? AND versions.version = ? AND documents.status <> ?", tenantID, documentID, versionNumber, DocumentStatusDeleted).
		First(version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return version, err
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

func (repository *GormRepository) CreateDelete(ctx context.Context, tenantID string, userID string, documentID string, job *model.KnowledgeJob, event *model.OutboxEvent) (*model.KnowledgeDocument, *model.KnowledgeJob, bool, error) {
	if repository == nil || repository.db == nil {
		return nil, nil, false, gorm.ErrInvalidDB
	}
	document := new(model.KnowledgeDocument)
	persistedJob := job
	duplicate := false
	err := repository.db.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND user_id = ?", documentID, tenantID, userID).First(document).Error; err != nil {
			return err
		}
		if document.Status == DocumentStatusDeleted {
			existing := new(model.KnowledgeJob)
			if err := transaction.Where("tenant_id = ? AND document_id = ? AND job_type = ?", tenantID, documentID, JobTypeDocumentDelete).
				Order("created_at DESC").First(existing).Error; err != nil {
				return err
			}
			persistedJob = existing
			duplicate = true
			return nil
		}
		var activeIndexJobs int64
		if err := transaction.Model(&model.KnowledgeJob{}).
			Where("tenant_id = ? AND document_id = ? AND job_type = ? AND status IN ?", tenantID, documentID, JobTypeDocumentIndex, []string{JobStatusQueued, JobStatusProcessing, JobStatusRetrying}).
			Count(&activeIndexJobs).Error; err != nil {
			return err
		}
		if activeIndexJobs > 0 {
			return ErrDocumentHasPendingIndex
		}
		job.TenantID, job.DocumentID, job.Version = tenantID, documentID, document.CurrentVersion
		event.TenantID, event.AggregateID, event.AggregateVersion = tenantID, documentID, document.CurrentVersion
		payload, marshalErr := json.Marshal(map[string]any{"job_id": job.ID, "document_id": documentID, "version": document.CurrentVersion})
		if marshalErr != nil {
			return marshalErr
		}
		event.PayloadJSON = string(payload)
		if err := transaction.Model(document).Updates(map[string]any{"status": DocumentStatusDeleted, "last_error_code": ""}).Error; err != nil {
			return err
		}
		document.Status = DocumentStatusDeleted
		if err := transaction.Create(job).Error; err != nil {
			return err
		}
		return transaction.Create(event).Error
	})
	return document, persistedJob, duplicate, err
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
