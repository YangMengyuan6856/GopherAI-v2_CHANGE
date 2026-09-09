package model

import "time"

// ControlIncident is the durable state for one bounded detector scope. It is
// intentionally global and low-cardinality: user, tenant, request and trace
// identifiers never enter the control plane.
type ControlIncident struct {
	IncidentKey      string     `gorm:"primaryKey;type:varchar(64)" json:"incident_key"`
	Metric           string     `gorm:"index;not null;type:varchar(64)" json:"metric"`
	Strategy         string     `gorm:"index;not null;type:varchar(64)" json:"strategy"`
	Status           string     `gorm:"index;not null;type:varchar(16)" json:"status"`
	Severity         string     `gorm:"not null;type:varchar(16)" json:"severity"`
	LastDecision     string     `gorm:"not null;type:varchar(32)" json:"last_decision"`
	LastBatchID      string     `gorm:"not null;type:char(64)" json:"last_batch_id"`
	LastRulesVersion string     `gorm:"not null;type:varchar(64)" json:"last_rules_version"`
	LastRulesSHA256  string     `gorm:"not null;type:char(64)" json:"last_rules_sha256"`
	LastValue        float64    `gorm:"type:double;not null" json:"last_value"`
	LastPopulation   int64      `gorm:"not null" json:"last_population"`
	RecoveryStreak   int        `gorm:"not null;default:0" json:"recovery_streak"`
	Version          uint64     `gorm:"not null;default:1" json:"version"`
	OpenedAt         time.Time  `gorm:"not null" json:"opened_at"`
	LastNotifiedAt   time.Time  `gorm:"not null" json:"last_notified_at"`
	ResolvedAt       *time.Time `json:"resolved_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// ControlWebhookDelivery is a transactional, durable delivery queue. A row in
// status=dead is the bounded DLQ; raw secrets and endpoint credentials are
// never persisted.
type ControlWebhookDelivery struct {
	EventID          string     `gorm:"primaryKey;type:char(64)" json:"event_id"`
	IncidentKey      string     `gorm:"index;not null;type:varchar(64)" json:"incident_key"`
	EventType        string     `gorm:"index;not null;type:varchar(24)" json:"event_type"`
	Simulation       bool       `gorm:"index;not null;default:false" json:"simulation"`
	PayloadJSON      string     `gorm:"type:longtext;not null" json:"-"`
	PayloadSHA256    string     `gorm:"not null;type:char(64)" json:"payload_sha256"`
	Status           string     `gorm:"index:idx_control_webhook_ready,priority:1;not null;type:varchar(16)" json:"status"`
	Attempt          int        `gorm:"not null;default:0" json:"attempt"`
	AvailableAt      time.Time  `gorm:"index:idx_control_webhook_ready,priority:2;not null" json:"available_at"`
	LeaseUntil       *time.Time `gorm:"index" json:"lease_until,omitempty"`
	DeliveredAt      *time.Time `json:"delivered_at,omitempty"`
	DeadLetteredAt   *time.Time `json:"dead_lettered_at,omitempty"`
	HTTPStatus       int        `gorm:"not null;default:0" json:"http_status"`
	LastErrorCode    string     `gorm:"not null;type:varchar(64)" json:"last_error_code,omitempty"`
	SignatureVersion string     `gorm:"not null;type:varchar(16)" json:"signature_version"`
	CreatedAt        time.Time  `gorm:"index;not null" json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// ControlWebhookReceipt is the receiver-side idempotency and signature audit.
// It stores only hashes and bounded metadata, never the webhook body.
type ControlWebhookReceipt struct {
	EventID          string    `gorm:"primaryKey;type:char(64)" json:"event_id"`
	EventType        string    `gorm:"index;not null;type:varchar(24)" json:"event_type"`
	Simulation       bool      `gorm:"index;not null;default:false" json:"simulation"`
	PayloadSHA256    string    `gorm:"not null;type:char(64)" json:"payload_sha256"`
	SignatureVersion string    `gorm:"not null;type:varchar(16)" json:"signature_version"`
	ReceivedAt       time.Time `gorm:"index;not null" json:"received_at"`
	CreatedAt        time.Time `json:"created_at"`
}
