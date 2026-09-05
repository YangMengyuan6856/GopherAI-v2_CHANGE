package controlwebhook

import "time"

const (
	SchemaVersion         = "control-webhook-v1"
	SignatureVersion      = "v1"
	StatusPending         = "pending"
	StatusProcessing      = "processing"
	StatusRetry           = "retry"
	StatusDelivered       = "delivered"
	StatusDead            = "dead"
	IncidentActive        = "active"
	IncidentResolved      = "resolved"
	EventOpened           = "opened"
	EventUpdated          = "updated"
	EventResolved         = "resolved"
	EventControlAction    = "control_action"
	EventRollback         = "rollback"
	ErrorTimeout          = "WEBHOOK_TIMEOUT"
	ErrorTransport        = "WEBHOOK_TRANSPORT_ERROR"
	ErrorRateLimited      = "WEBHOOK_RATE_LIMITED"
	ErrorRemoteServer     = "WEBHOOK_REMOTE_SERVER_ERROR"
	ErrorRemoteRejected   = "WEBHOOK_REMOTE_REJECTED"
	ErrorInvalidPayload   = "WEBHOOK_INVALID_PAYLOAD"
	ErrorInvalidSignature = "WEBHOOK_INVALID_SIGNATURE"
)

type Scope struct {
	Metric   string `json:"metric"`
	Strategy string `json:"strategy"`
}

type Observation struct {
	Value         float64   `json:"value"`
	Population    int       `json:"population"`
	WindowSeconds int       `json:"window_seconds"`
	ObservedAt    time.Time `json:"observed_at"`
}

type Detector struct {
	Version        string `json:"version"`
	DecisionStatus string `json:"decision_status"`
	FixedStatus    string `json:"fixed_status"`
	FixedReason    string `json:"fixed_reason"`
	ZScoreStatus   string `json:"z_score_status"`
	ZScoreReason   string `json:"z_score_reason"`
	ZeroVariance   bool   `json:"zero_variance"`
	Recommendation string `json:"recommendation"`
	Applied        bool   `json:"applied"`
}

type Provenance struct {
	BatchID      string `json:"batch_id"`
	RulesVersion string `json:"rules_version"`
	RulesSHA256  string `json:"rules_sha256"`
}

type Payload struct {
	SchemaVersion string      `json:"schema_version"`
	Simulation    bool        `json:"simulation"`
	FixtureMode   string      `json:"fixture_mode,omitempty"`
	EventID       string      `json:"event_id"`
	EventType     string      `json:"event_type"`
	IncidentKey   string      `json:"incident_key"`
	OccurredAt    time.Time   `json:"occurred_at"`
	Scope         Scope       `json:"scope"`
	Observation   Observation `json:"observation"`
	Detector      Detector    `json:"detector"`
	Provenance    Provenance  `json:"provenance"`
	Guardrails    []string    `json:"guardrails"`
}

type DeliverySummary struct {
	EventID          string     `json:"event_id"`
	IncidentKey      string     `json:"incident_key"`
	EventType        string     `json:"event_type"`
	Simulation       bool       `json:"simulation"`
	Status           string     `json:"status"`
	Attempt          int        `json:"attempt"`
	HTTPStatus       int        `json:"http_status"`
	LastErrorCode    string     `json:"last_error_code,omitempty"`
	PayloadSHA256    string     `json:"payload_sha256"`
	SignatureVersion string     `json:"signature_version"`
	CreatedAt        time.Time  `json:"created_at"`
	DeliveredAt      *time.Time `json:"delivered_at,omitempty"`
	DeadLetteredAt   *time.Time `json:"dead_lettered_at,omitempty"`
	ReceiptVerified  bool       `json:"receipt_verified"`
}

type AuditSnapshot struct {
	SchemaVersion string            `json:"schema_version"`
	Enabled       bool              `json:"enabled"`
	EndpointMode  string            `json:"endpoint_mode"`
	Signature     string            `json:"signature"`
	MaxAttempts   int               `json:"max_attempts"`
	Pending       int64             `json:"pending"`
	Processing    int64             `json:"processing"`
	Retrying      int64             `json:"retrying"`
	Delivered     int64             `json:"delivered"`
	DeadLettered  int64             `json:"dead_lettered"`
	Latest        []DeliverySummary `json:"latest"`
	Guardrails    []string          `json:"guardrails"`
	Limitations   []string          `json:"limitations"`
}

func validEventType(value string) bool {
	switch value {
	case EventOpened, EventUpdated, EventResolved, EventControlAction, EventRollback:
		return true
	default:
		return false
	}
}
