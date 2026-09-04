package incident

import (
	"context"
	"errors"
	"time"

	"GopherAI/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository interface {
	Confirm(context.Context, confirmationWrite) (model.ResolvedIncident, bool, error)
	GetByRun(context.Context, string, string) (*model.ResolvedIncident, error)
	GetByID(context.Context, string, string) (*model.ResolvedIncident, error)
	MarkIndexed(context.Context, string, int, time.Time) error
	RecordIndexFailure(context.Context, string, int, string, bool) error
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) Confirm(ctx context.Context, write confirmationWrite) (model.ResolvedIncident, bool, error) {
	if repository == nil || repository.db == nil {
		return model.ResolvedIncident{}, false, gorm.ErrInvalidDB
	}
	var result model.ResolvedIncident
	created := false
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		feedback := new(model.ResolutionFeedback)
		err := tx.Where("user_id_hash = ? AND client_request_id = ?", write.Feedback.UserIDHash, write.Feedback.ClientRequestID).First(feedback).Error
		if err == nil {
			if feedback.SourceRunID != write.Feedback.SourceRunID || feedback.HypothesisID != write.Feedback.HypothesisID || feedback.ResolutionSHA256 != write.ResolutionSHA256 {
				return ErrIdempotencyConflict
			}
			return tx.Where("feedback_id = ? AND user_id_hash = ?", feedback.ID, write.Feedback.UserIDHash).First(&result).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		existing := new(model.ResolvedIncident)
		err = tx.Where("user_id_hash = ? AND source_run_id = ?", write.Incident.UserIDHash, write.Incident.SourceRunID).First(existing).Error
		if err == nil {
			if existing.HypothesisID != write.Incident.HypothesisID || existing.Resolution != write.Incident.Resolution {
				return ErrAlreadyConfirmed
			}
			result = *existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		current := new(model.AgentLifecycleRun)
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("run_id = ? AND tenant_id_hash = ? AND user_id_hash = ?", write.Run.RunID, write.Run.TenantIDHash, write.Run.UserIDHash).
			First(current).Error; err != nil {
			return err
		}
		if current.State != "SUCCEEDED" || current.State != write.Run.State || current.StateVersion != write.Run.StateVersion {
			return ErrRunNotEligible
		}
		for _, value := range []any{&write.Feedback, &write.Incident, &write.Event} {
			if err := tx.Create(value).Error; err != nil {
				return err
			}
		}
		result, created = write.Incident, true
		return nil
	})
	if err == nil {
		return result, created, nil
	}
	// A concurrent replay can win the unique key race after our initial read.
	// Re-read by idempotency key and only convert it into success if the payload
	// is exactly equivalent.
	feedback := new(model.ResolutionFeedback)
	lookupErr := repository.db.WithContext(ctx).Where("user_id_hash = ? AND client_request_id = ?", write.Feedback.UserIDHash, write.Feedback.ClientRequestID).First(feedback).Error
	if lookupErr == nil {
		if feedback.SourceRunID != write.Feedback.SourceRunID || feedback.HypothesisID != write.Feedback.HypothesisID || feedback.ResolutionSHA256 != write.ResolutionSHA256 {
			return model.ResolvedIncident{}, false, ErrIdempotencyConflict
		}
		lookupErr = repository.db.WithContext(ctx).Where("feedback_id = ? AND user_id_hash = ?", feedback.ID, write.Feedback.UserIDHash).First(&result).Error
		return result, false, lookupErr
	}
	return model.ResolvedIncident{}, false, err
}

func (repository *GormRepository) GetByRun(ctx context.Context, userIDHash string, runID string) (*model.ResolvedIncident, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	incident := new(model.ResolvedIncident)
	err := repository.db.WithContext(ctx).Where("user_id_hash = ? AND source_run_id = ?", userIDHash, runID).First(incident).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return incident, err
}

func (repository *GormRepository) GetByID(ctx context.Context, tenantIDHash string, incidentID string) (*model.ResolvedIncident, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	incident := new(model.ResolvedIncident)
	err := repository.db.WithContext(ctx).Where("tenant_id_hash = ? AND id = ?", tenantIDHash, incidentID).First(incident).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return incident, err
}

func (repository *GormRepository) MarkIndexed(ctx context.Context, incidentID string, version int, indexedAt time.Time) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	result := repository.db.WithContext(ctx).Model(&model.ResolvedIncident{}).
		Where("id = ? AND version = ? AND status = ?", incidentID, version, StatusConfirmed).
		Updates(map[string]any{"index_status": IndexStatusIndexed, "index_error_code": "", "indexed_at": indexedAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (repository *GormRepository) RecordIndexFailure(ctx context.Context, incidentID string, version int, code string, exhausted bool) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	status := IndexStatusPending
	if exhausted {
		status = IndexStatusFailed
	}
	return repository.db.WithContext(ctx).Model(&model.ResolvedIncident{}).
		Where("id = ? AND version = ?", incidentID, version).
		Updates(map[string]any{"index_status": status, "index_error_code": code}).Error
}
