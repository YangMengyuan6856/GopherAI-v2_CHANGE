package evolution

import (
	"context"
	"errors"
	"sort"

	"GopherAI/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository interface {
	CreateArtifact(context.Context, model.HarnessArtifact) (bool, error)
	AppendReview(context.Context, model.HarnessArtifactReview) (bool, error)
	Audit(context.Context, int) (int64, int64, int64, int64, []model.HarnessArtifact, error)
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) CreateArtifact(ctx context.Context, artifact model.HarnessArtifact) (bool, error) {
	if repository == nil || repository.db == nil {
		return false, gorm.ErrInvalidDB
	}
	if err := ValidateCandidate(artifact); err != nil {
		return false, err
	}
	result := repository.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(&artifact)
	return result.RowsAffected == 1, result.Error
}

func (repository *GormRepository) AppendReview(ctx context.Context, review model.HarnessArtifactReview) (bool, error) {
	if repository == nil || repository.db == nil {
		return false, gorm.ErrInvalidDB
	}
	if err := validateReview(review); err != nil {
		return false, err
	}
	var artifactCount int64
	if err := repository.db.WithContext(ctx).Model(&model.HarnessArtifact{}).Where("id = ?", review.ArtifactID).Count(&artifactCount).Error; err != nil || artifactCount != 1 {
		return false, errors.New("review artifact does not exist")
	}
	result := repository.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(&review)
	return result.RowsAffected == 1, result.Error
}

func (repository *GormRepository) Audit(ctx context.Context, limit int) (int64, int64, int64, int64, []model.HarnessArtifact, error) {
	if repository == nil || repository.db == nil {
		return 0, 0, 0, 0, nil, gorm.ErrInvalidDB
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var artifacts, reviews, approved, applied int64
	if err := repository.db.WithContext(ctx).Model(&model.HarnessArtifact{}).Count(&artifacts).Error; err != nil {
		return 0, 0, 0, 0, nil, err
	}
	if err := repository.db.WithContext(ctx).Model(&model.HarnessArtifactReview{}).Count(&reviews).Error; err != nil {
		return 0, 0, 0, 0, nil, err
	}
	if err := repository.db.WithContext(ctx).Model(&model.HarnessArtifactReview{}).Where("decision = ?", "approved").Count(&approved).Error; err != nil {
		return 0, 0, 0, 0, nil, err
	}
	if err := repository.db.WithContext(ctx).Model(&model.HarnessArtifact{}).Where("applied = ?", true).Count(&applied).Error; err != nil {
		return 0, 0, 0, 0, nil, err
	}
	rows := make([]model.HarnessArtifact, 0, limit)
	if err := repository.db.WithContext(ctx).Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return 0, 0, 0, 0, nil, err
	}
	for _, row := range rows {
		if err := ValidateCandidate(row); err != nil {
			return 0, 0, 0, 0, nil, err
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	return artifacts, reviews, approved, applied, rows, nil
}

func validateReview(review model.HarnessArtifactReview) error {
	if len(review.ID) != 64 || len(review.ArtifactID) != 64 || len(review.ReviewerHash) != 64 || len(review.ReviewSHA256) != 64 || review.ID != review.ReviewSHA256 ||
		(review.Decision != "approved" && review.Decision != "rejected") || review.ReasonCode == "" || review.CreatedAt.IsZero() {
		return errors.New("harness artifact review is invalid")
	}
	return nil
}
