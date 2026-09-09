package model

import "time"

// AgentRun is the durable, sanitized execution record for one AppService request.
// Raw questions, prompts, answers, credentials, and user identifiers must not be
// stored here. Detailed payloads belong to their authoritative business tables.
type AgentRun struct {
	ID                    uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	TraceID               string    `gorm:"uniqueIndex;not null;type:varchar(36)" json:"trace_id"`
	RequestID             string    `gorm:"index;not null;type:varchar(128)" json:"request_id"`
	SessionID             string    `gorm:"index;type:varchar(36)" json:"session_id,omitempty"`
	UserIDHash            string    `gorm:"index;not null;type:char(64)" json:"user_id_hash"`
	Intent                string    `gorm:"index;not null;type:varchar(32)" json:"intent"`
	IntentVersion         string    `gorm:"type:varchar(64)" json:"intent_version"`
	FinalIntentStage      string    `gorm:"type:varchar(32)" json:"final_intent_stage"`
	ShadowIntent          string    `gorm:"index;type:varchar(32)" json:"shadow_intent,omitempty"`
	ShadowIntentVersion   string    `gorm:"type:varchar(64)" json:"shadow_intent_version,omitempty"`
	ShadowFinalStage      string    `gorm:"type:varchar(32)" json:"shadow_final_stage,omitempty"`
	ShadowReasonCode      string    `gorm:"type:varchar(64)" json:"shadow_reason_code,omitempty"`
	ShadowConfidence      float64   `json:"shadow_confidence,omitempty"`
	ShadowIsCompound      bool      `json:"shadow_is_compound,omitempty"`
	ShadowNeedsClarify    bool      `json:"shadow_needs_clarify,omitempty"`
	ShadowLatencyMicros   int64     `json:"shadow_latency_micros,omitempty"`
	ShadowPrototypeCalled bool      `json:"shadow_prototype_called,omitempty"`
	ShadowLLMCalled       bool      `json:"shadow_llm_called,omitempty"`
	ShadowLLMInputTokens  int       `json:"shadow_llm_input_tokens,omitempty"`
	ShadowLLMOutputTokens int       `json:"shadow_llm_output_tokens,omitempty"`
	Strategy              string    `gorm:"index;not null;type:varchar(64)" json:"strategy"`
	StrategyVersion       string    `gorm:"type:varchar(64)" json:"strategy_version"`
	PolicyVersion         string    `gorm:"index;type:varchar(64)" json:"policy_version"`
	Status                string    `gorm:"index;not null;type:varchar(16)" json:"status"`
	DurationMicros        int64     `gorm:"not null" json:"duration_micros"`
	InputTokens           int       `gorm:"not null" json:"input_tokens"`
	OutputTokens          int       `gorm:"not null" json:"output_tokens"`
	CostMicros            int64     `gorm:"not null" json:"cost_micros"`
	EvidenceCount         int       `gorm:"not null" json:"evidence_count"`
	ToolCallCount         int       `gorm:"not null" json:"tool_call_count"`
	ErrorCode             string    `gorm:"type:varchar(64)" json:"error_code,omitempty"`
	TraceEnvelopeJSON     string    `gorm:"type:longtext" json:"trace_envelope_json"`
	StartedAt             time.Time `gorm:"index;not null" json:"started_at"`
	FinishedAt            time.Time `gorm:"not null" json:"finished_at"`
	CreatedAt             time.Time `json:"created_at"`
}
