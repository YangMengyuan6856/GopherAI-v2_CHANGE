package knowledge

import (
	"GopherAI/internal/jobqueue"
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"
)

const DefaultMaxDeliveryAttempts = 3

type EventProcessor interface {
	Process(ctx context.Context, envelope jobqueue.Envelope) error
	Exhaust(ctx context.Context, envelope jobqueue.Envelope, code string) error
}

type IndexObserver interface {
	RecordIndexJob(status string, code string, duration time.Duration)
}

type IndexConsumer struct {
	processor   EventProcessor
	observer    IndexObserver
	maxAttempts int
	clock       Clock
}

func NewIndexConsumer(processor EventProcessor, observer IndexObserver, maxAttempts int, clock Clock) (*IndexConsumer, error) {
	if processor == nil || maxAttempts <= 0 || clock == nil {
		return nil, errors.New("processor, positive max attempts and clock are required")
	}
	return &IndexConsumer{processor: processor, observer: observer, maxAttempts: maxAttempts, clock: clock}, nil
}

func (consumer *IndexConsumer) Handle(ctx context.Context, body []byte) jobqueue.Result {
	startedAt := consumer.clock.Now()
	envelope := new(jobqueue.Envelope)
	if err := json.Unmarshal(body, envelope); err != nil {
		consumer.record("dead", ErrorCodeEventInvalid, startedAt)
		return jobqueue.Result{Action: jobqueue.ActionDead, Body: body, Code: ErrorCodeEventInvalid}
	}
	err := consumer.processor.Process(ctx, *envelope)
	if err == nil {
		consumer.record("success", "", startedAt)
		writeIndexLog(*envelope, "success", "")
		return jobqueue.Result{Action: jobqueue.ActionAck}
	}
	processingError := new(ProcessingError)
	if !errors.As(err, &processingError) {
		processingError = processError("INDEX_INTERNAL_ERROR", true, err)
	}
	if processingError.Retryable && envelope.Attempt+1 < consumer.maxAttempts {
		envelope.Attempt++
		retryBody, marshalErr := json.Marshal(envelope)
		if marshalErr == nil {
			consumer.record("retry", processingError.Code, startedAt)
			writeIndexLog(*envelope, "retry", processingError.Code)
			return jobqueue.Result{Action: jobqueue.ActionRetry, Body: retryBody, Code: processingError.Code}
		}
	}
	if processingError.Retryable {
		if exhaustErr := consumer.processor.Exhaust(ctx, *envelope, processingError.Code); exhaustErr != nil {
			envelope.Attempt++
			retryBody, _ := json.Marshal(envelope)
			consumer.record("retry", ErrorCodeIndexCompletion, startedAt)
			return jobqueue.Result{Action: jobqueue.ActionRetry, Body: retryBody, Code: ErrorCodeIndexCompletion}
		}
	}
	deadBody, marshalErr := json.Marshal(envelope)
	if marshalErr != nil {
		deadBody = body
	}
	consumer.record("dead", processingError.Code, startedAt)
	writeIndexLog(*envelope, "dead", processingError.Code)
	return jobqueue.Result{Action: jobqueue.ActionDead, Body: deadBody, Code: processingError.Code}
}

func (consumer *IndexConsumer) record(status string, code string, startedAt time.Time) {
	if consumer.observer != nil {
		consumer.observer.RecordIndexJob(status, code, consumer.clock.Now().Sub(startedAt))
	}
}

func writeIndexLog(envelope jobqueue.Envelope, status string, code string) {
	record := map[string]any{
		"event": "knowledge_index_job", "event_id": envelope.EventID,
		"trace_id": envelope.TraceID, "document_id": envelope.AggregateID,
		"attempt": envelope.Attempt, "status": status,
	}
	if code != "" {
		record["error_code"] = code
	}
	encoded, err := json.Marshal(record)
	if err == nil {
		log.Print(string(encoded))
	}
}
