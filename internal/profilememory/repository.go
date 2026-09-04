package profilememory

import (
	"context"
	"errors"
	"sort"
	"time"

	"GopherAI/model"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository interface {
	Capture(context.Context, string, string, string, []capturedFact, time.Time) error
	List(context.Context, string) ([]model.EnvironmentMemory, error)
	Correct(context.Context, string, string, string, *time.Time, time.Time) (model.EnvironmentMemory, error)
	Delete(context.Context, string, string) error
}

type capturedFact struct {
	Key        string
	Value      string
	Confidence float64
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) Capture(ctx context.Context, tenantHash string, userHash string, sourceRunID string, facts []capturedFact, now time.Time) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, fact := range facts {
			existingSource := new(model.EnvironmentMemory)
			err := tx.Where("user_id_hash = ? AND source_ref = ? AND source_key = ?", userHash, sourceRunID, fact.Key).First(existingSource).Error
			if err == nil {
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}

			var current []model.EnvironmentMemory
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("tenant_id_hash = ? AND user_id_hash = ? AND `key` = ? AND status IN ?", tenantHash, userHash, fact.Key, []string{StatusCandidate, StatusActive, StatusConflicted}).
				Find(&current).Error; err != nil {
				return err
			}
			sameValue := (*model.EnvironmentMemory)(nil)
			for index := range current {
				item := &current[index]
				if item.Value == fact.Value {
					sameValue = item
					break
				}
			}
			if sameValue != nil {
				expiresAt := now.Add(CandidateTTL)
				observation := model.EnvironmentMemory{
					ID: uuid.NewString(), TenantIDHash: tenantHash, UserIDHash: userHash, Key: fact.Key, Value: fact.Value,
					SourceType: SourceDiagnosticObservation, SourceRunID: sourceRunID, SourceRef: sourceRunID, SourceKey: fact.Key,
					Confidence: fact.Confidence, Status: StatusSuperseded, Version: sameValue.Version, ExpiresAt: &expiresAt,
					LastObservedAt: now, CreatedAt: now, UpdatedAt: now,
				}
				if err := tx.Create(&observation).Error; err != nil {
					return err
				}
				if err := tx.Model(&model.EnvironmentMemory{}).Where("id = ?", sameValue.ID).
					Updates(map[string]any{"last_observed_at": now, "updated_at": now}).Error; err != nil {
					return err
				}
				continue
			}
			status := StatusCandidate
			if len(current) > 0 {
				status = StatusConflicted
				if err := tx.Model(&model.EnvironmentMemory{}).
					Where("tenant_id_hash = ? AND user_id_hash = ? AND `key` = ? AND status IN ?", tenantHash, userHash, fact.Key, []string{StatusCandidate, StatusActive, StatusConflicted}).
					Updates(map[string]any{"status": StatusConflicted, "updated_at": now}).Error; err != nil {
					return err
				}
			}
			expiresAt := now.Add(CandidateTTL)
			memory := model.EnvironmentMemory{
				ID: uuid.NewString(), TenantIDHash: tenantHash, UserIDHash: userHash, Key: fact.Key, Value: fact.Value,
				SourceType: SourceDiagnosticObservation, SourceRunID: sourceRunID, SourceRef: sourceRunID, SourceKey: fact.Key,
				Confidence: fact.Confidence, Status: status, Version: 1, ExpiresAt: &expiresAt,
				LastObservedAt: now, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&memory).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (repository *GormRepository) List(ctx context.Context, userHash string) ([]model.EnvironmentMemory, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	var result []model.EnvironmentMemory
	err := repository.db.WithContext(ctx).Where("user_id_hash = ? AND status IN ?", userHash, []string{StatusActive, StatusCandidate, StatusConflicted}).Find(&result).Error
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Key != result[j].Key {
			return result[i].Key < result[j].Key
		}
		if result[i].Status != result[j].Status {
			return profileStatusRank(result[i].Status) < profileStatusRank(result[j].Status)
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, err
}

func (repository *GormRepository) Correct(ctx context.Context, userHash string, memoryID string, value string, expiresAt *time.Time, now time.Time) (model.EnvironmentMemory, error) {
	if repository == nil || repository.db == nil {
		return model.EnvironmentMemory{}, gorm.ErrInvalidDB
	}
	var corrected model.EnvironmentMemory
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		target := new(model.EnvironmentMemory)
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id_hash = ? AND status IN ?", memoryID, userHash, []string{StatusActive, StatusCandidate, StatusConflicted}).First(target).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrProfileMemoryNotFound
			}
			return err
		}
		if err := tx.Model(&model.EnvironmentMemory{}).
			Where("tenant_id_hash = ? AND user_id_hash = ? AND `key` = ? AND status IN ?", target.TenantIDHash, userHash, target.Key, []string{StatusActive, StatusCandidate, StatusConflicted}).
			Updates(map[string]any{"status": StatusSuperseded, "updated_at": now}).Error; err != nil {
			return err
		}
		corrected = model.EnvironmentMemory{
			ID: uuid.NewString(), TenantIDHash: target.TenantIDHash, UserIDHash: userHash, Key: target.Key, Value: value,
			SourceType: SourceUserCorrected, SourceRef: uuid.NewString(), SourceKey: target.Key, ParentID: target.ID,
			Confidence: 1, Status: StatusActive, Version: target.Version + 1, ExpiresAt: expiresAt,
			LastObservedAt: now, CreatedAt: now, UpdatedAt: now,
		}
		return tx.Create(&corrected).Error
	})
	return corrected, err
}

func (repository *GormRepository) Delete(ctx context.Context, userHash string, memoryID string) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	result := repository.db.WithContext(ctx).Where("id = ? AND user_id_hash = ?", memoryID, userHash).Delete(&model.EnvironmentMemory{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrProfileMemoryNotFound
	}
	return nil
}

func profileStatusRank(status string) int {
	switch status {
	case StatusActive:
		return 0
	case StatusConflicted:
		return 1
	default:
		return 2
	}
}
