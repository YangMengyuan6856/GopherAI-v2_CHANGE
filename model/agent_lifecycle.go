package model

import "time"

// AgentLifecycleRun is the durable execution aggregate for one resumable Agent
// request. User and tenant identifiers are hashed before persistence. Raw user
// input is not stored in this table.
type AgentLifecycleRun struct {
	RunID            string     `gorm:"primaryKey;type:char(36)" json:"run_id"`
	TenantIDHash     string     `gorm:"uniqueIndex:ux_agent_lifecycle_request,priority:1;index;not null;type:char(64)" json:"-"`
	UserIDHash       string     `gorm:"uniqueIndex:ux_agent_lifecycle_request,priority:2;index;not null;type:char(64)" json:"-"`
	ClientRequestID  string     `gorm:"uniqueIndex:ux_agent_lifecycle_request,priority:3;not null;type:varchar(128)" json:"client_request_id"`
	RequestID        string     `gorm:"index;not null;type:varchar(128)" json:"request_id"`
	TraceID          string     `gorm:"index;not null;type:char(36)" json:"trace_id"`
	SessionID        string     `gorm:"index;type:char(36)" json:"session_id,omitempty"`
	Intent           string     `gorm:"index;not null;type:varchar(32)" json:"intent"`
	Strategy         string     `gorm:"index;not null;type:varchar(64)" json:"strategy"`
	PolicyVersion    string     `gorm:"not null;type:varchar(64)" json:"policy_version"`
	HarnessVersion   string     `gorm:"not null;type:varchar(64)" json:"harness_version"`
	ContextVersion   string     `gorm:"not null;type:varchar(64)" json:"context_version"`
	State            string     `gorm:"index;not null;type:varchar(32)" json:"state"`
	StateVersion     int64      `gorm:"not null" json:"state_version"`
	CurrentStepID    string     `gorm:"type:varchar(64)" json:"current_step_id,omitempty"`
	NeedsUserInput   bool       `gorm:"not null" json:"needs_user_input"`
	TerminalReason   string     `gorm:"type:varchar(64)" json:"terminal_reason,omitempty"`
	LastErrorCode    string     `gorm:"type:varchar(64)" json:"last_error_code,omitempty"`
	LastCommandID    string     `gorm:"index;type:varchar(128)" json:"-"`
	LastCommandKind  string     `gorm:"type:varchar(32)" json:"-"`
	MaxIterations    int        `gorm:"not null" json:"max_iterations"`
	UsedIterations   int        `gorm:"not null" json:"used_iterations"`
	MaxToolCalls     int        `gorm:"not null" json:"max_tool_calls"`
	UsedToolCalls    int        `gorm:"not null" json:"used_tool_calls"`
	MaxInputTokens   int        `gorm:"not null" json:"max_input_tokens"`
	UsedInputTokens  int        `gorm:"not null" json:"used_input_tokens"`
	MaxOutputTokens  int        `gorm:"not null" json:"max_output_tokens"`
	UsedOutputTokens int        `gorm:"not null" json:"used_output_tokens"`
	MaxCostMicros    int64      `gorm:"not null" json:"max_cost_micros"`
	UsedCostMicros   int64      `gorm:"not null" json:"used_cost_micros"`
	StartedAt        time.Time  `gorm:"index;not null" json:"started_at"`
	DeadlineAt       time.Time  `gorm:"index;not null" json:"deadline_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// AgentLifecycleStep contains only public, auditable execution summaries. It
// must never contain hidden chain-of-thought or unredacted tool output.
type AgentLifecycleStep struct {
	ID               uint64     `gorm:"primaryKey;autoIncrement" json:"-"`
	RunID            string     `gorm:"uniqueIndex:ux_agent_lifecycle_step,priority:1;index;not null;type:char(36)" json:"run_id"`
	StepID           string     `gorm:"uniqueIndex:ux_agent_lifecycle_step,priority:2;not null;type:varchar(64)" json:"step_id"`
	Attempt          int        `gorm:"uniqueIndex:ux_agent_lifecycle_step,priority:3;not null" json:"attempt"`
	Kind             string     `gorm:"index;not null;type:varchar(32)" json:"kind"`
	Status           string     `gorm:"index;not null;type:varchar(24)" json:"status"`
	ReasonCode       string     `gorm:"type:varchar(64)" json:"reason_code,omitempty"`
	PublicSummary    string     `gorm:"type:varchar(500)" json:"public_summary,omitempty"`
	EvidenceRefsJSON string     `gorm:"type:text" json:"-"`
	ToolCallIDsJSON  string     `gorm:"type:text" json:"-"`
	BudgetDeltaJSON  string     `gorm:"type:text" json:"-"`
	ErrorCode        string     `gorm:"type:varchar(64)" json:"error_code,omitempty"`
	StateVersion     int64      `gorm:"index;not null" json:"state_version"`
	StartedAt        time.Time  `gorm:"not null" json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// AgentCheckpoint stores bounded, sanitized state needed to resume a run. The
// state hash makes accidental mutation detectable.
type AgentCheckpoint struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement" json:"-"`
	RunID          string    `gorm:"uniqueIndex:ux_agent_checkpoint_version,priority:1;index;not null;type:char(36)" json:"run_id"`
	StateVersion   int64     `gorm:"uniqueIndex:ux_agent_checkpoint_version,priority:2;not null" json:"state_version"`
	SchemaVersion  string    `gorm:"not null;type:varchar(32)" json:"schema_version"`
	HarnessVersion string    `gorm:"not null;type:varchar(64)" json:"harness_version"`
	ContextVersion string    `gorm:"not null;type:varchar(64)" json:"context_version"`
	StateHash      string    `gorm:"not null;type:char(64)" json:"state_hash"`
	StateJSON      string    `gorm:"not null;type:longtext" json:"-"`
	CreatedAt      time.Time `gorm:"index;not null" json:"created_at"`
}
