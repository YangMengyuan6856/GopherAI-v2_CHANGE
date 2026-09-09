package catalogreview

import (
	"context"
	"errors"

	"GopherAI/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository interface {
	ListLatest(context.Context, string, string, string) ([]model.EvaluationCatalogReview, error)
	Append(context.Context, model.EvaluationCatalogReview) (bool, model.EvaluationCatalogReview, error)
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) ListLatest(ctx context.Context, catalogSHA, governanceSHA, reviewerHash string) ([]model.EvaluationCatalogReview, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	rows := make([]model.EvaluationCatalogReview, 0, 320)
	err := repository.db.WithContext(ctx).Raw(`
SELECT reviews.*
FROM evaluation_catalog_reviews AS reviews
JOIN (
  SELECT case_id, MAX(revision) AS revision
  FROM evaluation_catalog_reviews
  WHERE catalog_sha256 = ? AND governance_sha256 = ? AND reviewer_hash = ?
  GROUP BY case_id
) AS latest ON latest.case_id = reviews.case_id AND latest.revision = reviews.revision
WHERE reviews.catalog_sha256 = ? AND reviews.governance_sha256 = ? AND reviews.reviewer_hash = ?
ORDER BY reviews.case_id ASC`, catalogSHA, governanceSHA, reviewerHash, catalogSHA, governanceSHA, reviewerHash).Scan(&rows).Error
	return rows, err
}

func (repository *GormRepository) Append(ctx context.Context, candidate model.EvaluationCatalogReview) (bool, model.EvaluationCatalogReview, error) {
	if repository == nil || repository.db == nil {
		return false, model.EvaluationCatalogReview{}, gorm.ErrInvalidDB
	}
	created := false
	stored := model.EvaluationCatalogReview{}
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var replay model.EvaluationCatalogReview
		result := tx.Where("idempotency_key_hash = ?", candidate.IdempotencyKeyHash).Limit(1).Find(&replay)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			if replay.RequestSHA256 != candidate.RequestSHA256 {
				return ErrIdempotencyConflict
			}
			stored = replay
			return nil
		}

		var latest model.EvaluationCatalogReview
		result = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("catalog_sha256 = ? AND governance_sha256 = ? AND case_id = ? AND reviewer_hash = ?", candidate.CatalogSHA256, candidate.GovernanceSHA256, candidate.CaseID, candidate.ReviewerHash).
			Order("revision DESC").Limit(1).Find(&latest)
		if result.Error != nil {
			return result.Error
		}
		currentRevision := 0
		if result.RowsAffected > 0 {
			currentRevision = latest.Revision
		}
		if currentRevision != candidate.ExpectedRevision || candidate.Revision != currentRevision+1 {
			return ErrRevisionConflict
		}
		if err := tx.Create(&candidate).Error; err != nil {
			return err
		}
		created, stored = true, candidate
		return nil
	})
	if errors.Is(err, ErrRevisionConflict) || errors.Is(err, ErrIdempotencyConflict) {
		return false, model.EvaluationCatalogReview{}, err
	}
	return created, stored, err
}
