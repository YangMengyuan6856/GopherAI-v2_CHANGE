package incident

import (
	"context"
	"encoding/json"
	"errors"

	"GopherAI/internal/jobqueue"
)

type Processor struct {
	repository Repository
	indexer    CaseIndexer
	clock      Clock
}

type indexPayload struct {
	IncidentID string `json:"incident_id"`
	Version    int    `json:"version"`
}

type ProcessingError struct {
	Code      string
	Retryable bool
	Cause     error
}

func (value *ProcessingError) Error() string { return value.Code }
func (value *ProcessingError) Unwrap() error { return value.Cause }

func NewProcessor(repository Repository, indexer CaseIndexer, clock Clock) (*Processor, error) {
	if repository == nil || indexer == nil || clock == nil {
		return nil, errors.New("incident repository, indexer and clock are required")
	}
	return &Processor{repository: repository, indexer: indexer, clock: clock}, nil
}

func (processor *Processor) Process(ctx context.Context, envelope jobqueue.Envelope) error {
	payload := new(indexPayload)
	if envelope.EventType != IndexEventType || envelope.TenantID == "" || envelope.AggregateID == "" || envelope.AggregateVersion <= 0 ||
		json.Unmarshal(envelope.Payload, payload) != nil || payload.IncidentID != envelope.AggregateID || payload.Version != envelope.AggregateVersion {
		return processError(ErrorEventInvalid, false, nil)
	}
	incident, err := processor.repository.GetByID(ctx, envelope.TenantID, payload.IncidentID)
	if err != nil {
		return processError(ErrorIncidentNotFound, true, err)
	}
	if incident == nil || incident.Version != payload.Version || incident.Status != StatusConfirmed {
		return processError(ErrorIncidentNotFound, false, nil)
	}
	if incident.IndexStatus == IndexStatusIndexed {
		return nil
	}
	if err := processor.indexer.Index(ctx, *incident); err != nil {
		_ = processor.repository.RecordIndexFailure(ctx, incident.ID, incident.Version, ErrorRedisIndex, false)
		return processError(ErrorRedisIndex, true, err)
	}
	if err := processor.repository.MarkIndexed(ctx, incident.ID, incident.Version, processor.clock.Now()); err != nil {
		_ = processor.repository.RecordIndexFailure(ctx, incident.ID, incident.Version, ErrorIndexCompletion, false)
		return processError(ErrorIndexCompletion, true, err)
	}
	return nil
}

func (processor *Processor) Exhaust(ctx context.Context, envelope jobqueue.Envelope, code string) error {
	payload := new(indexPayload)
	if json.Unmarshal(envelope.Payload, payload) != nil || payload.IncidentID != envelope.AggregateID || payload.Version != envelope.AggregateVersion {
		return processError(ErrorEventInvalid, false, nil)
	}
	return processor.repository.RecordIndexFailure(ctx, payload.IncidentID, payload.Version, code, true)
}

func processError(code string, retryable bool, cause error) *ProcessingError {
	return &ProcessingError{Code: code, Retryable: retryable, Cause: cause}
}
