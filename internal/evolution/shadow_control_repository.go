package evolution

import (
	"context"
	"errors"

	"GopherAI/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GormShadowControlRepository struct{ db *gorm.DB }

func NewGormShadowControlRepository(db *gorm.DB) *GormShadowControlRepository {
	return &GormShadowControlRepository{db: db}
}

func (repository *GormShadowControlRepository) HasHumanApproval(ctx context.Context, experimentVersion, candidateSHA, reportSHA string) (bool, error) {
	if repository == nil || repository.db == nil {
		return false, gorm.ErrInvalidDB
	}
	var count int64
	err := repository.db.WithContext(ctx).Model(&model.HarnessPromotionAttempt{}).
		Where("experiment_version = ? AND candidate_sha256 = ? AND report_sha256 = ? AND requested_decision = ? AND outcome = ? AND reason_code = ?", experimentVersion, candidateSHA, reportSHA, PromotionDecisionApprove, PromotionOutcomeRecorded, "human_approved_for_shadow").Count(&count).Error
	return count >= 1, err
}

func (repository *GormShadowControlRepository) AppendBlocked(ctx context.Context, event model.HarnessControlEvent) (bool, model.HarnessControlEvent, error) {
	if repository == nil || repository.db == nil {
		return false, model.HarnessControlEvent{}, gorm.ErrInvalidDB
	}
	if err := validateShadowControlEvent(event); err != nil || event.Outcome != ControlOutcomeBlocked {
		return false, model.HarnessControlEvent{}, ErrShadowControlInvalid
	}
	return repository.appendIdempotent(ctx, event)
}

func (repository *GormShadowControlRepository) ActivateShadow(ctx context.Context, draft model.HarnessControlEvent, candidate ControlCandidate) (bool, model.HarnessControlEvent, ActiveHarnessPointer, error) {
	if repository == nil || repository.db == nil || draft.Operation != ControlOperationShadow || !validControlCandidate(candidate) {
		return false, model.HarnessControlEvent{}, ActiveHarnessPointer{}, ErrShadowControlInvalid
	}
	var stored model.HarnessControlEvent
	var resultPointer ActiveHarnessPointer
	created := false
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, found, err := findControlEvent(tx, draft.IdempotencyKeyHash)
		if err != nil {
			return err
		}
		if found {
			if existing.RequestSHA256 != draft.RequestSHA256 {
				return ErrShadowControlIdempotent
			}
			stored = existing
			if existing.Outcome == ControlOutcomeBlocked {
				return nil
			}
			pointer, pointerFound, readErr := findPointer(tx, draft.ArtifactType, false)
			if readErr != nil {
				return readErr
			}
			if !pointerFound {
				return errors.New("applied shadow event has no active pointer")
			}
			resultPointer = activePointerFromModel(pointer)
			return nil
		}
		current, found, err := findPointer(tx, draft.ArtifactType, true)
		if err != nil {
			return err
		}
		before := ActiveHarnessPointer{ArtifactType: candidate.ArtifactType, CurrentVersion: candidate.ParentVersion, CurrentSHA256: candidate.ParentSHA256, StateVersion: 0, LastTransition: "implicit_baseline"}
		if found {
			before = activePointerFromModel(current)
		}
		next, err := ActivateHarnessPointer(before, candidate, ControlAdmission{OfflineGatePassed: true, HumanApproved: true, ShadowPassed: true, SafetyPassed: true}, draft.ExpectedStateVersion)
		if err != nil {
			return err
		}
		draft.Outcome, draft.ReasonCode, draft.PointerChanged = ControlOutcomeApplied, "isolated_shadow_activated", true
		draft.BeforeVersion, draft.BeforeSHA256, draft.BeforeStateVersion = before.CurrentVersion, before.CurrentSHA256, before.StateVersion
		draft.AfterVersion, draft.AfterSHA256, draft.AfterStateVersion = next.CurrentVersion, next.CurrentSHA256, next.StateVersion
		sealShadowControlEvent(&draft)
		if err := validateShadowControlEvent(draft); err != nil {
			return err
		}
		row := model.HarnessActivePointer{ID: pointerIdentity(ShadowPointerScope, next.ArtifactType), Scope: ShadowPointerScope, ArtifactType: next.ArtifactType, CurrentVersion: next.CurrentVersion, CurrentSHA256: next.CurrentSHA256, PreviousVersion: next.PreviousVersion, PreviousSHA256: next.PreviousSHA256, StateVersion: next.StateVersion, LastTransition: next.LastTransition, LastEventSHA256: draft.EventSHA256, UpdatedByHash: draft.ActorHash, UpdatedAt: draft.CreatedAt}
		if found {
			update := tx.Model(&model.HarnessActivePointer{}).Where("id = ? AND state_version = ?", current.ID, draft.ExpectedStateVersion).Updates(map[string]any{"current_version": row.CurrentVersion, "current_sha256": row.CurrentSHA256, "previous_version": row.PreviousVersion, "previous_sha256": row.PreviousSHA256, "state_version": row.StateVersion, "last_transition": row.LastTransition, "last_event_sha256": row.LastEventSHA256, "updated_by_hash": row.UpdatedByHash, "updated_at": row.UpdatedAt})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return ErrPointerStateConflict
			}
		} else if err := tx.Create(&row).Error; err != nil {
			return ErrPointerStateConflict
		}
		if err := tx.Create(&draft).Error; err != nil {
			return err
		}
		created, stored, resultPointer = true, draft, next
		return nil
	})
	return created, stored, resultPointer, err
}

func (repository *GormShadowControlRepository) RollbackShadow(ctx context.Context, draft model.HarnessControlEvent) (bool, model.HarnessControlEvent, ActiveHarnessPointer, error) {
	if repository == nil || repository.db == nil || draft.Operation != ControlOperationRollback {
		return false, model.HarnessControlEvent{}, ActiveHarnessPointer{}, ErrShadowControlInvalid
	}
	var stored model.HarnessControlEvent
	var resultPointer ActiveHarnessPointer
	created := false
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, foundEvent, err := findControlEvent(tx, draft.IdempotencyKeyHash)
		if err != nil {
			return err
		}
		if foundEvent {
			if existing.RequestSHA256 != draft.RequestSHA256 {
				return ErrShadowControlIdempotent
			}
			stored = existing
			if existing.Outcome == ControlOutcomeBlocked {
				return nil
			}
			pointer, foundPointer, readErr := findPointer(tx, draft.ArtifactType, false)
			if readErr != nil {
				return readErr
			}
			if !foundPointer {
				return errors.New("applied rollback event has no active pointer")
			}
			resultPointer = activePointerFromModel(pointer)
			return nil
		}
		current, foundPointer, err := findPointer(tx, draft.ArtifactType, true)
		if err != nil {
			return err
		}
		if !foundPointer {
			return ErrRollbackUnavailable
		}
		before := activePointerFromModel(current)
		next, err := RollbackHarnessPointer(before, draft.ExpectedStateVersion)
		if err != nil {
			return err
		}
		draft.Outcome, draft.ReasonCode, draft.PointerChanged = ControlOutcomeApplied, "rollback_completed", true
		draft.BeforeVersion, draft.BeforeSHA256, draft.BeforeStateVersion = before.CurrentVersion, before.CurrentSHA256, before.StateVersion
		draft.AfterVersion, draft.AfterSHA256, draft.AfterStateVersion = next.CurrentVersion, next.CurrentSHA256, next.StateVersion
		sealShadowControlEvent(&draft)
		if err := validateShadowControlEvent(draft); err != nil {
			return err
		}
		update := tx.Model(&model.HarnessActivePointer{}).Where("id = ? AND state_version = ?", current.ID, draft.ExpectedStateVersion).Updates(map[string]any{"current_version": next.CurrentVersion, "current_sha256": next.CurrentSHA256, "previous_version": "", "previous_sha256": "", "state_version": next.StateVersion, "last_transition": next.LastTransition, "last_event_sha256": draft.EventSHA256, "updated_by_hash": draft.ActorHash, "updated_at": draft.CreatedAt})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrPointerStateConflict
		}
		if err := tx.Create(&draft).Error; err != nil {
			return err
		}
		created, stored, resultPointer = true, draft, next
		return nil
	})
	return created, stored, resultPointer, err
}

func (repository *GormShadowControlRepository) AuditControl(ctx context.Context, limit int) (int64, int64, int64, []model.HarnessActivePointer, []model.HarnessControlEvent, error) {
	if repository == nil || repository.db == nil {
		return 0, 0, 0, nil, nil, gorm.ErrInvalidDB
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var total, applied, blocked int64
	for _, query := range []struct {
		value   *int64
		outcome string
	}{{&total, ""}, {&applied, ControlOutcomeApplied}, {&blocked, ControlOutcomeBlocked}} {
		db := repository.db.WithContext(ctx).Model(&model.HarnessControlEvent{})
		if query.outcome != "" {
			db = db.Where("outcome = ?", query.outcome)
		}
		if err := db.Count(query.value).Error; err != nil {
			return 0, 0, 0, nil, nil, err
		}
	}
	pointers := []model.HarnessActivePointer{}
	if err := repository.db.WithContext(ctx).Where("scope = ?", ShadowPointerScope).Order("artifact_type ASC").Find(&pointers).Error; err != nil {
		return 0, 0, 0, nil, nil, err
	}
	for _, pointer := range pointers {
		if err := validateShadowPointerModel(pointer); err != nil {
			return 0, 0, 0, nil, nil, errors.New("stored harness shadow pointer failed validation")
		}
	}
	events := []model.HarnessControlEvent{}
	if err := repository.db.WithContext(ctx).Order("created_at DESC, id DESC").Limit(limit).Find(&events).Error; err != nil {
		return 0, 0, 0, nil, nil, err
	}
	for _, event := range events {
		if err := validateShadowControlEvent(event); err != nil {
			return 0, 0, 0, nil, nil, errors.New("stored harness control event failed validation")
		}
	}
	return total, applied, blocked, pointers, events, nil
}

func (repository *GormShadowControlRepository) appendIdempotent(ctx context.Context, event model.HarnessControlEvent) (bool, model.HarnessControlEvent, error) {
	stored := model.HarnessControlEvent{}
	created := false
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, found, err := findControlEvent(tx, event.IdempotencyKeyHash)
		if err != nil {
			return err
		}
		if found {
			if existing.RequestSHA256 != event.RequestSHA256 {
				return ErrShadowControlIdempotent
			}
			stored = existing
			return nil
		}
		if err := tx.Create(&event).Error; err != nil {
			var concurrent model.HarnessControlEvent
			if readErr := tx.Where("idempotency_key_hash = ?", event.IdempotencyKeyHash).First(&concurrent).Error; readErr == nil {
				if concurrent.RequestSHA256 != event.RequestSHA256 {
					return ErrShadowControlIdempotent
				}
				stored = concurrent
				return nil
			}
			return err
		}
		created, stored = true, event
		return nil
	})
	return created, stored, err
}

func findControlEvent(tx *gorm.DB, idempotencyHash string) (model.HarnessControlEvent, bool, error) {
	row := model.HarnessControlEvent{}
	result := tx.Where("idempotency_key_hash = ?", idempotencyHash).Limit(1).Find(&row)
	return row, result.RowsAffected == 1, result.Error
}

func findPointer(tx *gorm.DB, artifactType string, lock bool) (model.HarnessActivePointer, bool, error) {
	row := model.HarnessActivePointer{}
	db := tx.Where("scope = ? AND artifact_type = ?", ShadowPointerScope, artifactType)
	if lock {
		db = db.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	result := db.Limit(1).Find(&row)
	return row, result.RowsAffected == 1, result.Error
}

func pointerIdentity(scope, artifactType string) string {
	return digestPromotion(scope + "\x00" + artifactType)
}

func validateShadowPointerModel(pointer model.HarnessActivePointer) error {
	previousValid := pointer.PreviousVersion == "" && pointer.PreviousSHA256 == "" || pointer.PreviousVersion != "" && len(pointer.PreviousSHA256) == 64
	if pointer.ID != pointerIdentity(pointer.Scope, pointer.ArtifactType) || pointer.Scope != ShadowPointerScope || pointer.ArtifactType == "" || pointer.CurrentVersion == "" || len(pointer.CurrentSHA256) != 64 || pointer.StateVersion == 0 || !previousValid || pointer.LastTransition == "" || len(pointer.LastEventSHA256) != 64 || len(pointer.UpdatedByHash) != 64 || pointer.UpdatedAt.IsZero() {
		return errors.New("harness shadow pointer is invalid")
	}
	return nil
}
