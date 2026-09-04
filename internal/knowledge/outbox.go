package knowledge

import (
	"GopherAI/internal/contract"
	"GopherAI/internal/jobqueue"
	"GopherAI/model"
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"gorm.io/gorm"
)

const (
	OutboxStatusPublished = "published"
	OutboxStatusFailed    = "failed"
	OutboxPayloadInvalid  = "OUTBOX_PAYLOAD_INVALID"
	OutboxPublishFailed   = "OUTBOX_PUBLISH_FAILED"
)

type OutboxRepository interface {
	PendingOutbox(ctx context.Context, now time.Time, limit int) ([]model.OutboxEvent, error)
	MarkOutboxPublished(ctx context.Context, eventID string, publishedAt time.Time) error
	RecordOutboxPublishFailure(ctx context.Context, event model.OutboxEvent, code string, nextAttempt time.Time) error
	MarkOutboxFailed(ctx context.Context, eventID string, code string) error
}

type OutboxObserver interface {
	RecordOutboxPublish(status string)
	SetOutboxOldestAge(seconds float64)
}

type OutboxPublisher struct {
	repository OutboxRepository
	publisher  jobqueue.Publisher
	observer   OutboxObserver
	clock      Clock
	interval   time.Duration
	batchSize  int
}

func NewOutboxPublisher(repository OutboxRepository, publisher jobqueue.Publisher, observer OutboxObserver, clock Clock, interval time.Duration, batchSize int) (*OutboxPublisher, error) {
	if repository == nil || publisher == nil || clock == nil || interval <= 0 || batchSize <= 0 {
		return nil, errors.New("outbox repository, publisher, clock, interval and batch size are required")
	}
	return &OutboxPublisher{repository: repository, publisher: publisher, observer: observer, clock: clock, interval: interval, batchSize: batchSize}, nil
}

func (publisher *OutboxPublisher) Run(ctx context.Context) error {
	ticker := time.NewTicker(publisher.interval)
	defer ticker.Stop()
	for {
		if _, err := publisher.PublishAvailable(ctx); err != nil && ctx.Err() == nil {
			log.Printf(`{"event":"outbox_publish_cycle","status":"error","error_code":"%s"}`, stableOutboxErrorCode(err))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (publisher *OutboxPublisher) PublishAvailable(ctx context.Context) (int, error) {
	now := publisher.clock.Now()
	events, err := publisher.repository.PendingOutbox(ctx, now, publisher.batchSize)
	if err != nil {
		return 0, err
	}
	if publisher.observer != nil {
		age := float64(0)
		if len(events) > 0 {
			age = now.Sub(events[0].CreatedAt).Seconds()
			if age < 0 {
				age = 0
			}
		}
		publisher.observer.SetOutboxOldestAge(age)
	}
	published := 0
	var cycleError error
	for _, event := range events {
		envelope, marshalErr := envelopeFromOutbox(event)
		if marshalErr != nil {
			_ = publisher.repository.MarkOutboxFailed(ctx, event.ID, OutboxPayloadInvalid)
			publisher.record("invalid")
			cycleError = errors.Join(cycleError, marshalErr)
			continue
		}
		body, marshalErr := json.Marshal(envelope)
		if marshalErr != nil {
			_ = publisher.repository.MarkOutboxFailed(ctx, event.ID, OutboxPayloadInvalid)
			publisher.record("invalid")
			cycleError = errors.Join(cycleError, marshalErr)
			continue
		}
		if publishErr := publisher.publisher.Publish(ctx, event.Topic, body); publishErr != nil {
			nextAttempt := now.Add(outboxBackoff(event.Attempt + 1))
			_ = publisher.repository.RecordOutboxPublishFailure(ctx, event, OutboxPublishFailed, nextAttempt)
			publisher.record("error")
			cycleError = errors.Join(cycleError, publishErr)
			continue
		}
		if markErr := publisher.repository.MarkOutboxPublished(ctx, event.ID, publisher.clock.Now()); markErr != nil {
			publisher.record("error")
			cycleError = errors.Join(cycleError, markErr)
			continue
		}
		publisher.record("published")
		published++
	}
	return published, cycleError
}

func (publisher *OutboxPublisher) record(status string) {
	if publisher.observer != nil {
		publisher.observer.RecordOutboxPublish(status)
	}
}

func envelopeFromOutbox(event model.OutboxEvent) (jobqueue.Envelope, error) {
	payload := json.RawMessage(event.PayloadJSON)
	if !json.Valid(payload) {
		return jobqueue.Envelope{}, errors.New("invalid outbox JSON payload")
	}
	return jobqueue.Envelope{
		SchemaVersion: contract.SchemaVersion,
		EventID:       event.ID, EventType: event.EventType, OccurredAt: event.CreatedAt,
		TraceID: event.TraceID, TenantID: event.TenantID, AggregateID: event.AggregateID,
		AggregateVersion: event.AggregateVersion, Attempt: 0, Payload: payload,
	}, nil
}

func outboxBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	return time.Duration(1<<uint(attempt-1)) * time.Second
}

func stableOutboxErrorCode(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "OUTBOX_CONTEXT_DONE"
	}
	return OutboxPublishFailed
}

func (repository *GormRepository) PendingOutbox(ctx context.Context, now time.Time, limit int) ([]model.OutboxEvent, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	var events []model.OutboxEvent
	err := repository.db.WithContext(ctx).Where("status = ? AND available_at <= ?", OutboxStatusPending, now).
		Order("created_at ASC").Limit(limit).Find(&events).Error
	return events, err
}

func (repository *GormRepository) MarkOutboxPublished(ctx context.Context, eventID string, publishedAt time.Time) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	return repository.db.WithContext(ctx).Model(&model.OutboxEvent{}).Where("id = ? AND status = ?", eventID, OutboxStatusPending).
		Updates(map[string]any{"status": OutboxStatusPublished, "published_at": publishedAt, "last_error_code": ""}).Error
}

func (repository *GormRepository) RecordOutboxPublishFailure(ctx context.Context, event model.OutboxEvent, code string, nextAttempt time.Time) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	return repository.db.WithContext(ctx).Model(&model.OutboxEvent{}).Where("id = ? AND status = ?", event.ID, OutboxStatusPending).
		Updates(map[string]any{"attempt": event.Attempt + 1, "available_at": nextAttempt, "last_error_code": code}).Error
}

func (repository *GormRepository) MarkOutboxFailed(ctx context.Context, eventID string, code string) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	return repository.db.WithContext(ctx).Model(&model.OutboxEvent{}).Where("id = ? AND status = ?", eventID, OutboxStatusPending).
		Updates(map[string]any{"status": OutboxStatusFailed, "last_error_code": code}).Error
}
