package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	Version           = "diagnostic-harness-v1"
	CheckpointVersion = "agent-checkpoint-v1"
	ContextVersion    = "diagnostic-context-v1"
)

type State string

const (
	StateReceived       State = "RECEIVED"
	StateContextReady   State = "CONTEXT_READY"
	StatePlanned        State = "PLANNED"
	StateRunning        State = "RUNNING"
	StateWaitingUser    State = "WAITING_USER"
	StateSucceeded      State = "SUCCEEDED"
	StateFailed         State = "FAILED"
	StateCancelled      State = "CANCELLED"
	StateBudgetExceeded State = "BUDGET_EXCEEDED"
)

var (
	ErrInvalidTransition = errors.New("invalid run state transition")
	ErrRunConflict       = errors.New("run state conflict")
	ErrRunNotFound       = errors.New("run not found")
	ErrBudgetExceeded    = errors.New("execution budget exceeded")
)

type Budget struct {
	MaxIterations    int   `json:"max_iterations"`
	UsedIterations   int   `json:"used_iterations"`
	MaxToolCalls     int   `json:"max_tool_calls"`
	UsedToolCalls    int   `json:"used_tool_calls"`
	MaxInputTokens   int   `json:"max_input_tokens"`
	UsedInputTokens  int   `json:"used_input_tokens"`
	MaxOutputTokens  int   `json:"max_output_tokens"`
	UsedOutputTokens int   `json:"used_output_tokens"`
	MaxCostMicros    int64 `json:"max_cost_micros"`
	UsedCostMicros   int64 `json:"used_cost_micros"`
}

func DefaultBudget() Budget {
	return Budget{MaxIterations: 6, MaxToolCalls: 4, MaxInputTokens: 16_000, MaxOutputTokens: 4_000, MaxCostMicros: 1_000_000}
}

func (budget Budget) Validate() error {
	if budget.MaxIterations < 1 || budget.MaxToolCalls < 0 || budget.MaxInputTokens < 1 || budget.MaxOutputTokens < 1 || budget.MaxCostMicros < 0 {
		return fmt.Errorf("invalid execution budget")
	}
	if budget.UsedIterations < 0 || budget.UsedToolCalls < 0 || budget.UsedInputTokens < 0 || budget.UsedOutputTokens < 0 || budget.UsedCostMicros < 0 {
		return fmt.Errorf("invalid used execution budget")
	}
	if budget.UsedIterations > budget.MaxIterations || budget.UsedToolCalls > budget.MaxToolCalls || budget.UsedInputTokens > budget.MaxInputTokens || budget.UsedOutputTokens > budget.MaxOutputTokens || budget.UsedCostMicros > budget.MaxCostMicros {
		return ErrBudgetExceeded
	}
	return nil
}

func IsTerminal(state State) bool {
	switch state {
	case StateSucceeded, StateFailed, StateCancelled, StateBudgetExceeded:
		return true
	default:
		return false
	}
}

func CanTransition(from State, to State) bool {
	allowed := map[State]map[State]bool{
		StateReceived:     {StateContextReady: true, StateFailed: true, StateCancelled: true},
		StateContextReady: {StatePlanned: true, StateFailed: true, StateCancelled: true},
		StatePlanned:      {StateRunning: true, StateFailed: true, StateCancelled: true},
		StateRunning:      {StateWaitingUser: true, StateSucceeded: true, StateFailed: true, StateCancelled: true, StateBudgetExceeded: true},
		StateWaitingUser:  {StateContextReady: true, StateCancelled: true},
	}
	return allowed[from][to]
}

func ValidateTransition(from State, to State) error {
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
	}
	return nil
}

type PublicStep struct {
	StepID        string      `json:"step_id"`
	Attempt       int         `json:"attempt"`
	Kind          string      `json:"kind"`
	Status        string      `json:"status"`
	ReasonCode    string      `json:"reason_code,omitempty"`
	PublicSummary string      `json:"public_summary,omitempty"`
	EvidenceRefs  []string    `json:"evidence_refs,omitempty"`
	ToolCallIDs   []string    `json:"tool_call_ids,omitempty"`
	BudgetDelta   BudgetDelta `json:"budget_delta"`
	ErrorCode     string      `json:"error_code,omitempty"`
	StateVersion  int64       `json:"state_version"`
	StartedAt     time.Time   `json:"started_at"`
	FinishedAt    *time.Time  `json:"finished_at,omitempty"`
}

type BudgetDelta struct {
	Iterations   int   `json:"iterations"`
	ToolCalls    int   `json:"tool_calls"`
	InputTokens  int   `json:"input_tokens"`
	OutputTokens int   `json:"output_tokens"`
	CostMicros   int64 `json:"cost_micros"`
}

func (step PublicStep) Validate() error {
	if strings.TrimSpace(step.StepID) == "" || len(step.StepID) > 64 || strings.TrimSpace(step.Kind) == "" || len(step.Kind) > 32 {
		return fmt.Errorf("invalid public step identity")
	}
	if step.Attempt < 1 || step.StateVersion < 1 {
		return fmt.Errorf("invalid public step version")
	}
	if len([]rune(step.PublicSummary)) > 500 {
		return fmt.Errorf("public step summary is too long")
	}
	return nil
}

type CheckpointState struct {
	Goal                string            `json:"goal"`
	Constraints         []string          `json:"constraints,omitempty"`
	ConfirmedFacts      map[string]string `json:"confirmed_facts,omitempty"`
	OpenQuestions       []string          `json:"open_questions,omitempty"`
	CompletedSteps      []string          `json:"completed_steps,omitempty"`
	FailedSteps         []string          `json:"failed_steps,omitempty"`
	EvidenceRefs        []string          `json:"evidence_refs,omitempty"`
	NextAction          string            `json:"next_action,omitempty"`
	LastActionSignature string            `json:"last_action_signature,omitempty"`
	RepeatedActionCount int               `json:"repeated_action_count,omitempty"`
	ArtifactType        string            `json:"artifact_type,omitempty"`
	Artifact            json.RawMessage   `json:"artifact,omitempty"`
}

type Run struct {
	RunID           string     `json:"run_id"`
	TenantIDHash    string     `json:"-"`
	UserIDHash      string     `json:"-"`
	ClientRequestID string     `json:"client_request_id"`
	RequestID       string     `json:"request_id"`
	TraceID         string     `json:"trace_id"`
	SessionID       string     `json:"session_id,omitempty"`
	Intent          string     `json:"intent"`
	Strategy        string     `json:"strategy"`
	PolicyVersion   string     `json:"policy_version"`
	HarnessVersion  string     `json:"harness_version"`
	ContextVersion  string     `json:"context_version"`
	State           State      `json:"state"`
	StateVersion    int64      `json:"state_version"`
	CurrentStepID   string     `json:"current_step_id,omitempty"`
	NeedsUserInput  bool       `json:"needs_user_input"`
	TerminalReason  string     `json:"terminal_reason,omitempty"`
	LastErrorCode   string     `json:"last_error_code,omitempty"`
	LastCommandID   string     `json:"-"`
	LastCommandKind string     `json:"-"`
	Budget          Budget     `json:"budget"`
	StartedAt       time.Time  `json:"started_at"`
	DeadlineAt      time.Time  `json:"deadline_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type RunDetail struct {
	Run        Run              `json:"run"`
	Steps      []PublicStep     `json:"steps"`
	Checkpoint *CheckpointState `json:"checkpoint,omitempty"`
}
