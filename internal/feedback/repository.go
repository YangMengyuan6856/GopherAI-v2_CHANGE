package feedback

import (
	"GopherAI/model"
	"context"
	"errors"

	"gorm.io/gorm"
)

type Repository interface {
	FindRun(context.Context, string, string) (model.AgentRun, error)
	Create(context.Context, *model.UserFeedbackEvent, *model.OnlineEvaluationSample, *model.OutboxEvent) (bool, string, string, error)
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) FindRun(ctx context.Context, userHash, requestID string) (model.AgentRun, error) {
	if repository == nil || repository.db == nil {
		return model.AgentRun{}, gorm.ErrInvalidDB
	}
	var run model.AgentRun
	err := repository.db.WithContext(ctx).
		Where("user_id_hash = ? AND request_id = ?", userHash, requestID).
		Order("created_at DESC").First(&run).Error
	return run, err
}

func (repository *GormRepository) Create(ctx context.Context, feedback *model.UserFeedbackEvent, sample *model.OnlineEvaluationSample, event *model.OutboxEvent) (bool, string, string, error) {
	if repository == nil || repository.db == nil || feedback == nil || sample == nil || event == nil {
		return false, "", "", gorm.ErrInvalidDB
	}
	created := false
	feedbackID := ""
	sampleID := ""
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.UserFeedbackEvent
		err := tx.Where("user_hash = ? AND request_hash = ? AND feedback_type = ?", feedback.UserHash, feedback.RequestHash, feedback.FeedbackType).First(&existing).Error
		if err == nil {
			feedbackID = existing.ID
			sampleID = existing.SampleID
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if sample.ID != feedback.SampleID || event.AggregateID != sample.ID || event.ID != sample.EventID {
			return errors.New("feedback transaction identity mismatch")
		}
		if err := tx.Create(sample).Error; err != nil {
			return err
		}
		if err := tx.Create(event).Error; err != nil {
			return err
		}
		if err := tx.Create(feedback).Error; err != nil {
			return err
		}
		created = true
		feedbackID = feedback.ID
		sampleID = sample.ID
		return nil
	})
	// A concurrent retry can pass the read check and lose the unique-index
	// race. Treat the committed first writer as the idempotent result instead
	// of surfacing a spurious failure to the user.
	if err != nil {
		var existing model.UserFeedbackEvent
		lookupErr := repository.db.WithContext(ctx).
			Where("user_hash = ? AND request_hash = ? AND feedback_type = ?", feedback.UserHash, feedback.RequestHash, feedback.FeedbackType).
			First(&existing).Error
		if lookupErr == nil {
			return false, existing.ID, existing.SampleID, nil
		}
	}
	return created, feedbackID, sampleID, err
}
