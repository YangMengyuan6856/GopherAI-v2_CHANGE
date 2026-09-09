package knowledge

import (
	"GopherAI/internal/jobqueue"
	"GopherAI/model"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type outboxMemoryRepository struct {
	events        []model.OutboxEvent
	published     []string
	failed        []string
	retryEvent    *model.OutboxEvent
	nextAttemptAt time.Time
}

func (repository *outboxMemoryRepository) PendingOutbox(context.Context, time.Time, int) ([]model.OutboxEvent, error) {
	return append([]model.OutboxEvent(nil), repository.events...), nil
}

func (repository *outboxMemoryRepository) MarkOutboxPublished(_ context.Context, eventID string, _ time.Time) error {
	repository.published = append(repository.published, eventID)
	return nil
}

func (repository *outboxMemoryRepository) RecordOutboxPublishFailure(_ context.Context, event model.OutboxEvent, _ string, nextAttempt time.Time) error {
	repository.retryEvent = &event
	repository.nextAttemptAt = nextAttempt
	return nil
}

func (repository *outboxMemoryRepository) MarkOutboxFailed(_ context.Context, eventID string, _ string) error {
	repository.failed = append(repository.failed, eventID)
	return nil
}

type outboxFakePublisher struct {
	routingKey string
	body       []byte
	err        error
}

func (publisher *outboxFakePublisher) Publish(_ context.Context, routingKey string, body []byte) error {
	publisher.routingKey = routingKey
	publisher.body = append([]byte(nil), body...)
	return publisher.err
}

type outboxFakeObserver struct {
	statuses []string
	age      float64
}

func (observer *outboxFakeObserver) RecordOutboxPublish(status string) {
	observer.statuses = append(observer.statuses, status)
}

func (observer *outboxFakeObserver) SetOutboxOldestAge(seconds float64) { observer.age = seconds }

func TestOutboxPublisherPublishesEnvelopeBeforeMarkingEvent(t *testing.T) {
	now := time.Date(2026, 9, 4, 3, 0, 0, 0, time.UTC)
	repository := &outboxMemoryRepository{events: []model.OutboxEvent{{
		ID: "event-1", Topic: DocumentIndexTopic, EventType: DocumentIndexEventType,
		TraceID: "trace-1", TenantID: "tenant-a", AggregateID: "document-1", AggregateVersion: 1,
		PayloadJSON: `{"job_id":"job-1","document_id":"document-1","version":1}`,
		Status:      OutboxStatusPending, CreatedAt: now.Add(-10 * time.Second),
	}}}
	broker := new(outboxFakePublisher)
	observer := new(outboxFakeObserver)
	publisher, err := NewOutboxPublisher(repository, broker, observer, fixedClock{value: now}, time.Second, 20)
	if err != nil {
		t.Fatal(err)
	}
	published, err := publisher.PublishAvailable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if published != 1 || len(repository.published) != 1 || repository.published[0] != "event-1" || broker.routingKey != DocumentIndexTopic {
		t.Fatalf("unexpected publish result: published=%d marked=%v routing=%q", published, repository.published, broker.routingKey)
	}
	var envelope jobqueue.Envelope
	if err := json.Unmarshal(broker.body, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.EventID != "event-1" || envelope.TraceID != "trace-1" || envelope.TenantID != "tenant-a" || observer.age != 10 || observer.statuses[0] != "published" {
		t.Fatalf("unexpected envelope or observer: %+v %+v", envelope, observer)
	}
}

func TestOutboxPublisherRetainsEventWhenBrokerFails(t *testing.T) {
	now := time.Date(2026, 9, 4, 3, 0, 0, 0, time.UTC)
	repository := &outboxMemoryRepository{events: []model.OutboxEvent{{
		ID: "event-1", Topic: DocumentIndexTopic, EventType: DocumentIndexEventType,
		PayloadJSON: `{}`, Status: OutboxStatusPending, CreatedAt: now, Attempt: 2,
	}}}
	broker := &outboxFakePublisher{err: errors.New("rabbit unavailable")}
	publisher, err := NewOutboxPublisher(repository, broker, nil, fixedClock{value: now}, time.Second, 20)
	if err != nil {
		t.Fatal(err)
	}
	published, err := publisher.PublishAvailable(context.Background())
	if err == nil || published != 0 {
		t.Fatalf("expected retained publish failure, got published=%d err=%v", published, err)
	}
	if len(repository.published) != 0 || repository.retryEvent == nil || repository.retryEvent.ID != "event-1" || !repository.nextAttemptAt.After(now) {
		t.Fatalf("event was not retained for retry: %+v", repository)
	}
}

func TestOutboxPublisherQuarantinesInvalidPayload(t *testing.T) {
	repository := &outboxMemoryRepository{events: []model.OutboxEvent{{ID: "bad-event", Topic: DocumentIndexTopic, PayloadJSON: `{not-json`, Status: OutboxStatusPending}}}
	publisher, err := NewOutboxPublisher(repository, new(outboxFakePublisher), nil, fixedClock{value: time.Now().UTC()}, time.Second, 20)
	if err != nil {
		t.Fatal(err)
	}
	_, err = publisher.PublishAvailable(context.Background())
	if err == nil || len(repository.failed) != 1 || repository.failed[0] != "bad-event" {
		t.Fatalf("invalid payload was not quarantined: failed=%v err=%v", repository.failed, err)
	}
}
