package incident

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"GopherAI/internal/jobqueue"
	"GopherAI/model"
)

type processorRepository struct {
	incident  *model.ResolvedIncident
	indexed   int
	failures  int
	exhausted bool
}

func (*processorRepository) Confirm(context.Context, confirmationWrite) (model.ResolvedIncident, bool, error) {
	return model.ResolvedIncident{}, false, nil
}
func (*processorRepository) GetByRun(context.Context, string, string) (*model.ResolvedIncident, error) {
	return nil, nil
}
func (repository *processorRepository) GetByID(context.Context, string, string) (*model.ResolvedIncident, error) {
	return repository.incident, nil
}
func (repository *processorRepository) MarkIndexed(context.Context, string, int, time.Time) error {
	repository.indexed++
	return nil
}
func (repository *processorRepository) RecordIndexFailure(_ context.Context, _ string, _ int, _ string, exhausted bool) error {
	repository.failures++
	repository.exhausted = exhausted
	return nil
}

type fakeIndexer struct {
	err   error
	calls int
}

func (indexer *fakeIndexer) Index(context.Context, model.ResolvedIncident) error {
	indexer.calls++
	return indexer.err
}

func incidentEnvelope() jobqueue.Envelope {
	payload, _ := json.Marshal(map[string]any{"incident_id": "incident-1", "version": 1})
	return jobqueue.Envelope{EventType: IndexEventType, TenantID: "tenant-hash", AggregateID: "incident-1", AggregateVersion: 1, Payload: payload}
}

func TestProcessorIndexesOnlyConfirmedIncident(t *testing.T) {
	repository := &processorRepository{incident: &model.ResolvedIncident{ID: "incident-1", TenantIDHash: "tenant-hash", UserIDHash: "user-hash", Version: 1, Status: StatusConfirmed, IndexStatus: IndexStatusPending}}
	indexer := new(fakeIndexer)
	processor, _ := NewProcessor(repository, indexer, fixedClock{value: time.Now().UTC()})
	if err := processor.Process(context.Background(), incidentEnvelope()); err != nil {
		t.Fatal(err)
	}
	if indexer.calls != 1 || repository.indexed != 1 {
		t.Fatalf("confirmed incident was not indexed exactly once: index=%d completion=%d", indexer.calls, repository.indexed)
	}
	repository.incident.Status = "candidate"
	if err := processor.Process(context.Background(), incidentEnvelope()); err == nil {
		t.Fatal("candidate incident unexpectedly indexed")
	}
	if indexer.calls != 1 {
		t.Fatal("candidate incident reached indexer")
	}
}

func TestConsumerRetriesThenExhaustsIndexFailure(t *testing.T) {
	repository := &processorRepository{incident: &model.ResolvedIncident{ID: "incident-1", TenantIDHash: "tenant-hash", UserIDHash: "user-hash", Version: 1, Status: StatusConfirmed, IndexStatus: IndexStatusPending}}
	processor, _ := NewProcessor(repository, &fakeIndexer{err: errors.New("redis unavailable")}, fixedClock{value: time.Now().UTC()})
	consumer, _ := NewConsumer(processor, nil, 2, fixedClock{value: time.Now().UTC()})
	envelope := incidentEnvelope()
	body, _ := json.Marshal(envelope)
	first := consumer.Handle(context.Background(), body)
	if first.Action != jobqueue.ActionRetry {
		t.Fatalf("expected retry, got %s", first.Action)
	}
	second := consumer.Handle(context.Background(), first.Body)
	if second.Action != jobqueue.ActionDead || !repository.exhausted {
		t.Fatalf("expected exhausted dead-letter, got %s exhausted=%v", second.Action, repository.exhausted)
	}
}
