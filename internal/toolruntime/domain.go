package toolruntime

import (
	"context"
	"encoding/json"
	"time"
)

const SchemaVersion = "tool-message-v1"

type SideEffect string

const (
	SideEffectReadOnly      SideEffect = "read_only"
	SideEffectInternalWrite SideEffect = "internal_write"
	SideEffectExternalWrite SideEffect = "external_write"
)

type PropertySchema struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	MinLength   int      `json:"min_length,omitempty"`
	MaxLength   int      `json:"max_length,omitempty"`
}

// InputSchema is the deliberately small JSON-Schema subset accepted by the
// runtime. Keeping the subset explicit makes validation deterministic and
// prevents an adapter from silently weakening argument validation.
type InputSchema struct {
	Type                 string                    `json:"type"`
	Properties           map[string]PropertySchema `json:"properties"`
	Required             []string                  `json:"required,omitempty"`
	AdditionalProperties bool                      `json:"additional_properties"`
}

type Definition struct {
	Name               string      `json:"name"`
	Version            string      `json:"version"`
	Description        string      `json:"description"`
	InputSchema        InputSchema `json:"input_schema"`
	AllowedIntents     []string    `json:"allowed_intents"`
	RequiredPermission string      `json:"required_permission"`
	SideEffect         SideEffect  `json:"side_effect"`
	TimeoutMS          int64       `json:"timeout_ms"`
	MaxResultBytes     int         `json:"max_result_bytes"`
}

type Principal struct {
	TenantID    string
	UserID      string
	Permissions map[string]bool
}

type CallBudget struct {
	MaxCalls  int `json:"max_calls"`
	UsedCalls int `json:"used_calls"`
}

type Invocation struct {
	CallID            string
	TraceID           string
	ToolName          string
	Arguments         json.RawMessage
	Intent            string
	Strategy          string
	Principal         Principal
	AllowedSideEffect SideEffect
	Budget            CallBudget
}

type Output struct {
	Data         any
	EvidenceRefs []string
	Retryable    bool
}

// ToolMessage is the only result shape allowed to leave the governed runtime.
// It intentionally excludes raw arguments, credentials and internal errors.
type ToolMessage struct {
	CallID       string          `json:"call_id"`
	ToolName     string          `json:"tool_name"`
	ToolVersion  string          `json:"tool_version"`
	ArgsHash     string          `json:"args_hash"`
	Status       string          `json:"status"`
	Data         json.RawMessage `json:"data,omitempty"`
	EvidenceRefs []string        `json:"evidence_refs,omitempty"`
	ErrorCode    string          `json:"error_code,omitempty"`
	Retryable    bool            `json:"retryable"`
	LatencyMS    int64           `json:"latency_ms"`
	Cached       bool            `json:"cached"`
	Truncated    bool            `json:"truncated"`
}

const (
	StatusSuccess        = "success"
	StatusRejected       = "rejected"
	StatusInvalidArgs    = "invalid_arguments"
	StatusBudgetExceeded = "budget_exceeded"
	StatusTimeout        = "timeout"
	StatusCancelled      = "cancelled"
	StatusError          = "error"
)

const (
	ErrorToolNotRegistered = "TOOL_NOT_REGISTERED"
	ErrorIntentDenied      = "TOOL_INTENT_DENIED"
	ErrorPermissionDenied  = "TOOL_PERMISSION_DENIED"
	ErrorSideEffectDenied  = "TOOL_SIDE_EFFECT_DENIED"
	ErrorArgumentsInvalid  = "TOOL_ARGUMENTS_INVALID"
	ErrorBudgetExceeded    = "TOOL_BUDGET_EXCEEDED"
	ErrorTimeout           = "TOOL_TIMEOUT"
	ErrorCancelled         = "TOOL_CANCELLED"
	ErrorExecutionFailed   = "TOOL_EXECUTION_FAILED"
	ErrorResultTooLarge    = "TOOL_RESULT_TOO_LARGE"
)

type Tool interface {
	Definition() Definition
	Execute(context.Context, map[string]any) (Output, error)
}

type Auditor interface {
	Record(context.Context, Invocation, ToolMessage) error
}

type Observer interface {
	RecordToolValidation(tool string, result string)
	RecordToolCall(tool string, strategy string, status string, duration time.Duration)
	RecordToolCancellation(tool string, reason string)
	RecordToolAuditFailure(tool string)
}

type nopAuditor struct{}

func (nopAuditor) Record(context.Context, Invocation, ToolMessage) error { return nil }

type nopObserver struct{}

func (nopObserver) RecordToolValidation(string, string)                  {}
func (nopObserver) RecordToolCall(string, string, string, time.Duration) {}
func (nopObserver) RecordToolCancellation(string, string)                {}
func (nopObserver) RecordToolAuditFailure(string)                        {}
