package knowledge

import (
	"GopherAI/internal/jobqueue"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type consumerProcessor struct {
	err         error
	processCall int
	exhaustCode string
	exhaustErr  error
}

func (processor *consumerProcessor) Process(context.Context, jobqueue.Envelope) error {
	processor.processCall++
	return processor.err
}

func (processor *consumerProcessor) Exhaust(_ context.Context, _ jobqueue.Envelope, code string) error {
	processor.exhaustCode = code
	return processor.exhaustErr
}

type consumerObserver struct {
	status string
	code   string
}

func (observer *consumerObserver) RecordIndexJob(status string, code string, _ time.Duration) {
	observer.status = status
	observer.code = code
}

func TestIndexConsumerAcknowledgesSuccess(t *testing.T) {
	observer := new(consumerObserver)
	consumer, err := NewIndexConsumer(new(consumerProcessor), observer, DefaultMaxDeliveryAttempts, fixedClock{value: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	result := consumer.Handle(context.Background(), envelopeBody(t, 0))
	if result.Action != jobqueue.ActionAck || observer.status != "success" {
		t.Fatalf("unexpected result: %+v observer=%+v", result, observer)
	}
}

func TestIndexConsumerRetriesWithIncrementedAttempt(t *testing.T) {
	processor := &consumerProcessor{err: processError(ErrorCodeRedisIndex, true, errors.New("redis unavailable"))}
	consumer, err := NewIndexConsumer(processor, nil, DefaultMaxDeliveryAttempts, fixedClock{value: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	result := consumer.Handle(context.Background(), envelopeBody(t, 0))
	if result.Action != jobqueue.ActionRetry || result.Code != ErrorCodeRedisIndex {
		t.Fatalf("unexpected retry result: %+v", result)
	}
	var envelope jobqueue.Envelope
	if err := json.Unmarshal(result.Body, &envelope); err != nil || envelope.Attempt != 1 {
		t.Fatalf("retry attempt was not incremented: %+v, %v", envelope, err)
	}
}

func TestIndexConsumerExhaustsRetryableFailureIntoDeadLetter(t *testing.T) {
	processor := &consumerProcessor{err: processError(ErrorCodeRedisIndex, true, errors.New("redis unavailable"))}
	consumer, err := NewIndexConsumer(processor, nil, DefaultMaxDeliveryAttempts, fixedClock{value: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	result := consumer.Handle(context.Background(), envelopeBody(t, 2))
	if result.Action != jobqueue.ActionDead || processor.exhaustCode != ErrorCodeRedisIndex {
		t.Fatalf("exhausted event was not dead-lettered: %+v processor=%+v", result, processor)
	}
}

func TestIndexConsumerDeadLettersMalformedJSON(t *testing.T) {
	processor := new(consumerProcessor)
	consumer, err := NewIndexConsumer(processor, nil, DefaultMaxDeliveryAttempts, fixedClock{value: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	result := consumer.Handle(context.Background(), []byte(`{bad`))
	if result.Action != jobqueue.ActionDead || processor.processCall != 0 {
		t.Fatalf("malformed event should not reach processor: %+v", result)
	}
}

func envelopeBody(t *testing.T, attempt int) []byte {
	t.Helper()
	envelope := indexEnvelope(t)
	envelope.Attempt = attempt
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
