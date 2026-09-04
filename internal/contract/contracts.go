package contract

import (
	"fmt"
	"strings"
	"time"
)

const SchemaVersion = "1"

type RequestContext struct {
	TraceID           string           `json:"trace_id"`
	RequestID         string           `json:"request_id"`
	UserID            string           `json:"user_id"`
	TenantID          string           `json:"tenant_id"`
	SessionID         string           `json:"session_id,omitempty"`
	Question          string           `json:"question"`
	KnowledgeRequired bool             `json:"knowledge_required,omitempty"`
	Locale            string           `json:"locale"`
	StartedAt         time.Time        `json:"started_at"`
	Deadline          time.Time        `json:"deadline"`
	PolicyVersion     string           `json:"policy_version,omitempty"`
	Debug             bool             `json:"debug"`
	Budgets           ExecutionBudgets `json:"budgets"`
}

func (request RequestContext) Validate() error {
	switch {
	case strings.TrimSpace(request.TraceID) == "":
		return fmt.Errorf("trace_id is required")
	case strings.TrimSpace(request.RequestID) == "":
		return fmt.Errorf("request_id is required")
	case strings.TrimSpace(request.UserID) == "":
		return fmt.Errorf("user_id is required")
	case strings.TrimSpace(request.Question) == "":
		return fmt.Errorf("question is required")
	case request.StartedAt.IsZero():
		return fmt.Errorf("started_at is required")
	case request.Deadline.IsZero():
		return fmt.Errorf("deadline is required")
	case !request.Deadline.After(request.StartedAt):
		return fmt.Errorf("deadline must be after started_at")
	}
	return request.Budgets.Validate()
}

type ExecutionBudgets struct {
	MaxAgents       int           `json:"max_agents"`
	MaxToolCalls    int           `json:"max_tool_calls"`
	MaxIterations   int           `json:"max_iterations"`
	MaxInputTokens  int           `json:"max_input_tokens"`
	MaxOutputTokens int           `json:"max_output_tokens"`
	MaxCostMicros   int64         `json:"max_cost_micros"`
	TotalTimeout    time.Duration `json:"total_timeout"`
}

func (budgets ExecutionBudgets) Validate() error {
	if budgets.MaxAgents < 1 {
		return fmt.Errorf("max_agents must be positive")
	}
	if budgets.TotalTimeout <= 0 {
		return fmt.Errorf("total_timeout must be positive")
	}
	return nil
}

type IntentStageResult struct {
	Stage      string  `json:"stage"`
	Intent     string  `json:"intent"`
	Confidence float64 `json:"confidence"`
	ReasonCode string  `json:"reason_code,omitempty"`
}

type IntentResult struct {
	Intent       string              `json:"intent"`
	Confidence   float64             `json:"confidence"`
	Entities     map[string]string   `json:"entities,omitempty"`
	IsCompound   bool                `json:"is_compound"`
	NeedsClarify bool                `json:"needs_clarify"`
	Stages       []IntentStageResult `json:"stages,omitempty"`
	Version      string              `json:"version"`
}

type Evidence struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	TenantID      string  `json:"tenant_id"`
	SourceID      string  `json:"source_id"`
	SourceVersion string  `json:"source_version"`
	Title         string  `json:"title"`
	Section       string  `json:"section,omitempty"`
	LineStart     int     `json:"line_start,omitempty"`
	LineEnd       int     `json:"line_end,omitempty"`
	Content       string  `json:"content,omitempty"`
	Score         float64 `json:"score"`
	Retrieval     string  `json:"retrieval"`
	ContentHash   string  `json:"content_hash"`
}

type Citation struct {
	ID         string `json:"citation_id"`
	EvidenceID string `json:"evidence_id"`
	Document   string `json:"document"`
	Version    string `json:"version"`
	Section    string `json:"section,omitempty"`
	LineStart  int    `json:"line_start,omitempty"`
	LineEnd    int    `json:"line_end,omitempty"`
}

type ToolCallResult struct {
	ToolName  string         `json:"tool_name"`
	Version   string         `json:"version"`
	Status    string         `json:"status"`
	Output    map[string]any `json:"output,omitempty"`
	ErrorCode string         `json:"error_code,omitempty"`
	Latency   time.Duration  `json:"latency"`
	Cached    bool           `json:"cached"`
}

type ModelUsage struct {
	InputTokens  int   `json:"input_tokens"`
	OutputTokens int   `json:"output_tokens"`
	CostMicros   int64 `json:"cost_micros"`
}

type AgentResult struct {
	SessionID         string           `json:"session_id,omitempty"`
	Answer            string           `json:"answer"`
	Citations         []Citation       `json:"citations,omitempty"`
	Evidence          []Evidence       `json:"evidence,omitempty"`
	Confidence        float64          `json:"confidence"`
	Resolved          bool             `json:"resolved"`
	NeedsUserInput    bool             `json:"needs_user_input"`
	FollowUpQuestions []string         `json:"follow_up_questions,omitempty"`
	ToolCalls         []ToolCallResult `json:"tool_calls,omitempty"`
	Usage             ModelUsage       `json:"usage"`
	Error             *DomainError     `json:"error,omitempty"`
}

type StrategyDecision struct {
	StrategyName     string           `json:"strategy_name"`
	StrategyVersion  string           `json:"strategy_version"`
	PolicyVersion    string           `json:"policy_version"`
	ReasonCode       string           `json:"reason_code"`
	ExperimentBucket string           `json:"experiment_bucket,omitempty"`
	Fallbacks        []string         `json:"fallbacks,omitempty"`
	Budgets          ExecutionBudgets `json:"budgets"`
}

func (decision StrategyDecision) Validate() error {
	if strings.TrimSpace(decision.StrategyName) == "" {
		return fmt.Errorf("strategy_name is required")
	}
	if strings.TrimSpace(decision.StrategyVersion) == "" {
		return fmt.Errorf("strategy_version is required")
	}
	if strings.TrimSpace(decision.PolicyVersion) == "" {
		return fmt.Errorf("policy_version is required")
	}
	return decision.Budgets.Validate()
}

type TraceStep struct {
	Name       string         `json:"name"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt time.Time      `json:"finished_at"`
	Status     string         `json:"status"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type TraceEnvelope struct {
	SchemaVersion string           `json:"schema_version"`
	TraceID       string           `json:"trace_id"`
	RequestID     string           `json:"request_id"`
	SessionID     string           `json:"session_id,omitempty"`
	Intent        IntentResult     `json:"intent"`
	Decision      StrategyDecision `json:"decision"`
	StartedAt     time.Time        `json:"started_at"`
	FinishedAt    time.Time        `json:"finished_at,omitempty"`
	Steps         []TraceStep      `json:"steps"`
	Error         *DomainError     `json:"error,omitempty"`
}

type EvalRecord struct {
	ID             string             `json:"id"`
	TraceID        string             `json:"trace_id"`
	DatasetVersion string             `json:"dataset_version"`
	CaseID         string             `json:"case_id"`
	Candidate      string             `json:"candidate"`
	Scores         map[string]float64 `json:"scores"`
	Passed         bool               `json:"passed"`
	CreatedAt      time.Time          `json:"created_at"`
}

type StreamEventType string

const (
	StreamEventMeta     StreamEventType = "meta"
	StreamEventDelta    StreamEventType = "delta"
	StreamEventCitation StreamEventType = "citation"
	StreamEventFinal    StreamEventType = "final"
	StreamEventError    StreamEventType = "error"
)

type ShadowIntentSummary struct {
	Intent          string   `json:"intent"`
	Confidence      float64  `json:"confidence"`
	FinalStage      string   `json:"final_stage"`
	Version         string   `json:"version"`
	ReasonCodes     []string `json:"reason_codes,omitempty"`
	IsCompound      bool     `json:"is_compound"`
	NeedsClarify    bool     `json:"needs_clarify"`
	PrototypeCalled bool     `json:"prototype_called"`
	LLMCalled       bool     `json:"llm_called"`
	LatencyMillis   int64    `json:"latency_ms"`
	Mode            string   `json:"mode"`
}

type StreamEvent struct {
	Type           StreamEventType      `json:"-"`
	SchemaVersion  string               `json:"schema_version"`
	TraceID        string               `json:"trace_id"`
	RequestID      string               `json:"request_id"`
	SessionID      string               `json:"session_id,omitempty"`
	Intent         string               `json:"intent,omitempty"`
	Strategy       string               `json:"strategy,omitempty"`
	PolicyVersion  string               `json:"policy_version,omitempty"`
	Text           string               `json:"text,omitempty"`
	Citation       *Citation            `json:"citation,omitempty"`
	Confidence     float64              `json:"confidence,omitempty"`
	NeedsUserInput bool                 `json:"needs_user_input,omitempty"`
	Usage          *ModelUsage          `json:"usage,omitempty"`
	Error          *DomainError         `json:"error,omitempty"`
	IntentShadow   *ShadowIntentSummary `json:"intent_shadow,omitempty"`
}
