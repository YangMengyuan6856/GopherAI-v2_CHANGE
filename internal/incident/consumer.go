package incident

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"GopherAI/internal/jobqueue"
)

type EventProcessor interface {
	Process(context.Context, jobqueue.Envelope) error
	Exhaust(context.Context, jobqueue.Envelope, string) error
}

type IndexObserver interface {
	RecordIncidentIndex(string, string, time.Duration)
}

type Consumer struct {
	processor   EventProcessor
	observer    IndexObserver
	maxAttempts int
	clock       Clock
}

func NewConsumer(processor EventProcessor, observer IndexObserver, maxAttempts int, clock Clock) (*Consumer, error) {
	if processor == nil || maxAttempts <= 0 || clock == nil {
		return nil, errors.New("processor, positive maximum attempts and clock are required")
	}
	return &Consumer{processor: processor, observer: observer, maxAttempts: maxAttempts, clock: clock}, nil
}

func (consumer *Consumer) Handle(ctx context.Context, body []byte) jobqueue.Result {
	started := consumer.clock.Now()
	envelope := new(jobqueue.Envelope)
	if err := json.Unmarshal(body, envelope); err != nil {
		consumer.record("dead", ErrorEventInvalid, started)
		return jobqueue.Result{Action: jobqueue.ActionDead, Body: body, Code: ErrorEventInvalid}
	}
	err := consumer.processor.Process(ctx, *envelope)
	if err == nil {
		consumer.record("success", "", started)
		consumer.writeLog(*envelope, "success", "")
		return jobqueue.Result{Action: jobqueue.ActionAck}
	}
	processing := new(ProcessingError)
	if !errors.As(err, &processing) {
		processing = processError(ErrorIndexCompletion, true, err)
	}
	if processing.Retryable && envelope.Attempt+1 < consumer.maxAttempts {
		envelope.Attempt++
		if retryBody, marshalErr := json.Marshal(envelope); marshalErr == nil {
			consumer.record("retry", processing.Code, started)
			consumer.writeLog(*envelope, "retry", processing.Code)
			return jobqueue.Result{Action: jobqueue.ActionRetry, Body: retryBody, Code: processing.Code}
		}
	}
	if processing.Retryable {
		if exhaustErr := consumer.processor.Exhaust(ctx, *envelope, processing.Code); exhaustErr != nil {
			envelope.Attempt++
			retryBody, _ := json.Marshal(envelope)
			consumer.record("retry", ErrorIndexCompletion, started)
			return jobqueue.Result{Action: jobqueue.ActionRetry, Body: retryBody, Code: ErrorIndexCompletion}
		}
	}
	deadBody, marshalErr := json.Marshal(envelope)
	if marshalErr != nil {
		deadBody = body
	}
	consumer.record("dead", processing.Code, started)
	consumer.writeLog(*envelope, "dead", processing.Code)
	return jobqueue.Result{Action: jobqueue.ActionDead, Body: deadBody, Code: processing.Code}
}

func (consumer *Consumer) record(status string, code string, started time.Time) {
	if consumer.observer != nil {
		consumer.observer.RecordIncidentIndex(status, code, consumer.clock.Now().Sub(started))
	}
}

func (consumer *Consumer) writeLog(envelope jobqueue.Envelope, status string, code string) {
	record := map[string]any{"event": "incident_index_job", "event_id": envelope.EventID, "trace_id": envelope.TraceID, "incident_id": envelope.AggregateID, "attempt": envelope.Attempt, "status": status}
	if code != "" {
		record["error_code"] = code
	}
	if encoded, err := json.Marshal(record); err == nil {
		log.Print(string(encoded))
	}
}
