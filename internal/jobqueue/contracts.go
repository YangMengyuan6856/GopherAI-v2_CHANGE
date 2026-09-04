package jobqueue

import (
	"context"
	"encoding/json"
	"time"
)

type Envelope struct {
	SchemaVersion    string          `json:"schema_version"`
	EventID          string          `json:"event_id"`
	EventType        string          `json:"event_type"`
	OccurredAt       time.Time       `json:"occurred_at"`
	TraceID          string          `json:"trace_id"`
	TenantID         string          `json:"tenant_id"`
	AggregateID      string          `json:"aggregate_id"`
	AggregateVersion int             `json:"aggregate_version"`
	Attempt          int             `json:"attempt"`
	Payload          json.RawMessage `json:"payload"`
}

type Publisher interface {
	Publish(ctx context.Context, routingKey string, body []byte) error
}

type Consumer interface {
	Consume(ctx context.Context, handler func(context.Context, []byte) Result) error
}

type Action string

const (
	ActionAck   Action = "ack"
	ActionRetry Action = "retry"
	ActionDead  Action = "dead"
)

type Result struct {
	Action Action
	Body   []byte
	Code   string
}
