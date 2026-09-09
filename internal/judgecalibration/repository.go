package judgecalibration

import (
	"context"
	"errors"

	"GopherAI/model"

	"gorm.io/gorm"
)

type Repository interface {
	ListLatest(context.Context, string, string) ([]model.JudgeCalibrationReview, error)
	Append(context.Context, *model.JudgeCalibrationReview) (bool, model.JudgeCalibrationReview, error)
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) ListLatest(ctx context.Context, datasetSHA, reviewerHash string) ([]model.JudgeCalibrationReview, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	rows := make([]model.JudgeCalibrationReview, 0, 30)
	err := repository.db.WithContext(ctx).Raw(`
SELECT reviews.*
FROM judge_calibration_reviews AS reviews
JOIN (
  SELECT case_id, MAX(revision) AS revision
  FROM judge_calibration_reviews
  WHERE dataset_sha256 = ? AND reviewer_hash = ?
  GROUP BY case_id
) AS latest ON latest.case_id = reviews.case_id AND latest.revision = reviews.revision
WHERE reviews.dataset_sha256 = ? AND reviews.reviewer_hash = ?
ORDER BY reviews.case_id ASC`, datasetSHA, reviewerHash, datasetSHA, reviewerHash).Scan(&rows).Error
	return rows, err
}

func (repository *GormRepository) Append(ctx context.Context, review *model.JudgeCalibrationReview) (bool, model.JudgeCalibrationReview, error) {
	if repository == nil || repository.db == nil || review == nil {
		return false, model.JudgeCalibrationReview{}, gorm.ErrInvalidDB
	}
	created := false
	stored := model.JudgeCalibrationReview{}
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var latest model.JudgeCalibrationReview
		result := tx.Where("dataset_sha256 = ? AND case_id = ? AND reviewer_hash = ?", review.DatasetSHA256, review.CaseID, review.ReviewerHash).
			Order("revision DESC").Limit(1).Find(&latest)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 && latest.ReviewSHA256 == review.ReviewSHA256 {
			stored = latest
			return nil
		}
		review.Revision = latest.Revision + 1
		if review.Revision < 1 {
			return errors.New("judge review revision is invalid")
		}
		if err := tx.Create(review).Error; err != nil {
			return err
		}
		created, stored = true, *review
		return nil
	})
	return created, stored, err
}
