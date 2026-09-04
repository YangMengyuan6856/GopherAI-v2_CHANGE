package model

import "time"

// ToolAudit is a sanitized, append-only record. Raw tool arguments, outputs,
// prompts, credentials and principal identifiers must never be stored here.
type ToolAudit struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	CallID         string    `gorm:"index;not null;type:varchar(128)" json:"call_id"`
	TraceID        string    `gorm:"index;not null;type:varchar(36)" json:"trace_id"`
	TenantIDHash   string    `gorm:"index;not null;type:char(64)" json:"tenant_id_hash"`
	UserIDHash     string    `gorm:"index;not null;type:char(64)" json:"user_id_hash"`
	ToolName       string    `gorm:"index;not null;type:varchar(64)" json:"tool_name"`
	ToolVersion    string    `gorm:"type:varchar(32)" json:"tool_version"`
	ArgsHash       string    `gorm:"not null;type:char(64)" json:"args_hash"`
	Intent         string    `gorm:"index;not null;type:varchar(32)" json:"intent"`
	Strategy       string    `gorm:"index;not null;type:varchar(64)" json:"strategy"`
	Status         string    `gorm:"index;not null;type:varchar(32)" json:"status"`
	ErrorCode      string    `gorm:"type:varchar(64)" json:"error_code,omitempty"`
	Retryable      bool      `json:"retryable"`
	LatencyMS      int64     `gorm:"not null" json:"latency_ms"`
	Cached         bool      `json:"cached"`
	Stale          bool      `json:"stale"`
	DegradedReason string    `gorm:"type:varchar(64)" json:"degraded_reason,omitempty"`
	Truncated      bool      `json:"truncated"`
	CreatedAt      time.Time `gorm:"index;not null" json:"created_at"`
}
