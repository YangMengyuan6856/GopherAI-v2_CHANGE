package evolution

import (
	"context"
	"errors"

	"GopherAI/model"

	"gorm.io/gorm"
)

type GormPromotionAuthorizer struct{ db *gorm.DB }

func NewGormPromotionAuthorizer(db *gorm.DB) *GormPromotionAuthorizer {
	return &GormPromotionAuthorizer{db: db}
}

func (authorizer *GormPromotionAuthorizer) CanReview(ctx context.Context, principal string) (bool, error) {
	if authorizer == nil || authorizer.db == nil {
		return false, gorm.ErrInvalidDB
	}
	var count int64
	err := authorizer.db.WithContext(ctx).Model(&model.User{}).Where("username = ? AND role = ? AND deleted_at IS NULL", principal, PromotionReviewerRole).Count(&count).Error
	return count == 1, err
}

type GormPromotionRepository struct{ db *gorm.DB }

func NewGormPromotionRepository(db *gorm.DB) *GormPromotionRepository {
	return &GormPromotionRepository{db: db}
}

func (repository *GormPromotionRepository) AppendAttempt(ctx context.Context, attempt model.HarnessPromotionAttempt) (bool, model.HarnessPromotionAttempt, error) {
	if repository == nil || repository.db == nil {
		return false, model.HarnessPromotionAttempt{}, gorm.ErrInvalidDB
	}
	if err := validatePromotionAttempt(attempt); err != nil {
		return false, model.HarnessPromotionAttempt{}, err
	}
	created := false
	stored := model.HarnessPromotionAttempt{}
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("idempotency_key_hash = ?", attempt.IdempotencyKeyHash).Limit(1).Find(&stored)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			if stored.AttemptSHA256 != attempt.AttemptSHA256 {
				return ErrPromotionIdempotency
			}
			return nil
		}
		if err := tx.Create(&attempt).Error; err != nil {
			var concurrent model.HarnessPromotionAttempt
			if readErr := tx.Where("idempotency_key_hash = ?", attempt.IdempotencyKeyHash).First(&concurrent).Error; readErr == nil {
				if concurrent.AttemptSHA256 != attempt.AttemptSHA256 {
					return ErrPromotionIdempotency
				}
				stored = concurrent
				return nil
			}
			return err
		}
		created, stored = true, attempt
		return nil
	})
	return created, stored, err
}

func (repository *GormPromotionRepository) AuditAttempts(ctx context.Context, limit int) (int64, int64, int64, int64, []model.HarnessPromotionAttempt, error) {
	if repository == nil || repository.db == nil {
		return 0, 0, 0, 0, nil, gorm.ErrInvalidDB
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var total, recorded, blocked, rejected int64
	queries := []struct {
		value *int64
		where string
		arg   string
	}{{&total, "", ""}, {&recorded, "outcome = ?", PromotionOutcomeRecorded}, {&blocked, "outcome = ?", PromotionOutcomeBlocked}, {&rejected, "requested_decision = ? AND outcome = ?", PromotionDecisionReject}}
	for index, query := range queries {
		db := repository.db.WithContext(ctx).Model(&model.HarnessPromotionAttempt{})
		if index == 3 {
			db = db.Where(query.where, query.arg, PromotionOutcomeRecorded)
		} else if query.where != "" {
			db = db.Where(query.where, query.arg)
		}
		if err := db.Count(query.value).Error; err != nil {
			return 0, 0, 0, 0, nil, err
		}
	}
	rows := make([]model.HarnessPromotionAttempt, 0, limit)
	if err := repository.db.WithContext(ctx).Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return 0, 0, 0, 0, nil, err
	}
	for _, row := range rows {
		if err := validatePromotionAttempt(row); err != nil {
			return 0, 0, 0, 0, nil, errors.New("stored harness promotion attempt failed validation")
		}
	}
	return total, recorded, blocked, rejected, rows, nil
}
