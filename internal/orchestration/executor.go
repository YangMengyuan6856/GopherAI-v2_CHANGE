package orchestration

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"GopherAI/internal/contract"
	"GopherAI/internal/diagnostic"
)

const (
	ExecutionSchemaVersion = "collaboration-execution-shadow-v1"
	ExecutorVersion        = "bounded-parallel-executor-v1"
	TaskStatusSucceeded    = "succeeded"
	TaskStatusFailed       = "failed"
	TaskStatusTimedOut     = "timed_out"
	TaskStatusCancelled    = "cancelled"
	TaskStatusBudget       = "budget_exceeded"
	ExecutionCompleted     = "completed"
	ExecutionPartial       = "partial"
	ExecutionCancelled     = "cancelled"
	ExecutionBudget        = "budget_exceeded"
	ExecutionFailed        = "failed"
	maxAgentClaims         = 10
	maxAgentEvidence       = 20
	maxAgentFollowUps      = 5
	maxAgentSummaryRunes   = 4000
)

type ExecutionInput struct {
	TenantID string `json:"-"`
	UserID   string `json:"-"`
	Message  string `json:"-"`
}

type SharedEvidence struct {
	ID            string  `json:"id"`
	SourceType    string  `json:"source_type"`
	Summary       string  `json:"summary"`
	TenantID      string  `json:"-"`
	SourceID      string  `json:"source_id,omitempty"`
	SourceVersion string  `json:"source_version,omitempty"`
	LineStart     int     `json:"line_start,omitempty"`
	LineEnd       int     `json:"line_end,omitempty"`
	ContentHash   string  `json:"content_hash,omitempty"`
	Score         float64 `json:"score"`
}

type AgentClaim struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Statement    string   `json:"statement"`
	EvidenceRefs []string `json:"evidence_refs"`
	Confidence   float64  `json:"confidence"`
}

type AgentOutput struct {
	Summary      string              `json:"summary"`
	Claims       []AgentClaim        `json:"claims"`
	Evidence     []SharedEvidence    `json:"evidence"`
	FollowUps    []string            `json:"follow_ups"`
	Usage        contract.ModelUsage `json:"usage"`
	ToolCalls    int                 `json:"tool_calls"`
	Iterations   int                 `json:"iterations"`
	OutputReason string              `json:"output_reason"`
}

type AgentRunner interface {
	Run(context.Context, PlannedTask, ExecutionInput) (AgentOutput, error)
}

type TaskExecution struct {
	Index      int         `json:"index"`
	TaskID     string      `json:"task_id"`
	Agent      string      `json:"agent"`
	Status     string      `json:"status"`
	ReasonCode string      `json:"reason_code"`
	DurationMS int64       `json:"duration_ms"`
	Output     AgentOutput `json:"output"`
}

type BudgetUsage struct {
	Agents       int   `json:"agents"`
	ToolCalls    int   `json:"tool_calls"`
	Iterations   int   `json:"iterations"`
	InputTokens  int   `json:"input_tokens"`
	OutputTokens int   `json:"output_tokens"`
	CostMicros   int64 `json:"cost_micros"`
}

type ExecutionResult struct {
	SchemaVersion      string          `json:"schema_version"`
	ExecutorVersion    string          `json:"executor_version"`
	Mode               string          `json:"mode"`
	AffectsLiveTraffic bool            `json:"affects_live_traffic"`
	Status             string          `json:"status"`
	ReasonCode         string          `json:"reason_code"`
	TaskResults        []TaskExecution `json:"task_results"`
	Usage              BudgetUsage     `json:"usage"`
	DurationMS         int64           `json:"duration_ms"`
	InputRedactions    int             `json:"input_redactions"`
}

type ParallelExecutor struct {
	runners map[string]AgentRunner
}

func NewParallelExecutor(runners map[string]AgentRunner) (*ParallelExecutor, error) {
	if len(runners) == 0 {
		return nil, errors.New("at least one agent runner is required")
	}
	copyOfRunners := make(map[string]AgentRunner, len(runners))
	for name, runner := range runners {
		if name != KnowledgeAgentRole && name != DiagnosticAgentRole {
			return nil, errors.New("unknown agent runner")
		}
		if runner == nil {
			return nil, errors.New("agent runner is required")
		}
		copyOfRunners[name] = runner
	}
	return &ParallelExecutor{runners: copyOfRunners}, nil
}

type taskResultEnvelope struct {
	result TaskExecution
}

func (executor *ParallelExecutor) Execute(parent context.Context, plan CollaborationPlan, input ExecutionInput) (ExecutionResult, error) {
	startedAt := time.Now()
	result := ExecutionResult{
		SchemaVersion: ExecutionSchemaVersion, ExecutorVersion: ExecutorVersion,
		Mode: "shadow_only", AffectsLiveTraffic: false, Status: ExecutionFailed, ReasonCode: "execution_not_started",
		TaskResults: []TaskExecution{},
	}
	if executor == nil || len(executor.runners) == 0 {
		return result, errors.New("parallel executor is unavailable")
	}
	if err := plan.validate(); err != nil {
		result.ReasonCode = "plan_invalid"
		return result, err
	}
	input.TenantID, input.UserID, input.Message = strings.TrimSpace(input.TenantID), strings.TrimSpace(input.UserID), strings.TrimSpace(input.Message)
	if input.TenantID == "" || input.UserID == "" || input.Message == "" {
		result.ReasonCode = "execution_input_invalid"
		return result, errors.New("tenant, user and message are required")
	}
	extracted, err := (diagnostic.Extractor{}).Extract(input.Message)
	if err != nil {
		result.ReasonCode = "execution_input_invalid"
		return result, err
	}
	input.Message = extracted.SanitizedExcerpt
	result.InputRedactions = extracted.RedactionCount
	if err := parent.Err(); err != nil {
		result.Status, result.ReasonCode = ExecutionCancelled, "caller_cancelled"
		return result, err
	}
	totalContext, cancel := context.WithTimeout(parent, time.Duration(plan.Budget.TotalTimeoutMS)*time.Millisecond)
	defer cancel()
	results := make(chan taskResultEnvelope, len(plan.Tasks))
	for _, task := range plan.Tasks {
		task := task
		runner := executor.runners[task.Agent]
		go func() {
			results <- taskResultEnvelope{result: executeTask(totalContext, task, input, runner)}
		}()
	}
	collected := make(map[int]TaskExecution, len(plan.Tasks))
	for len(collected) < len(plan.Tasks) {
		select {
		case envelope := <-results:
			collected[envelope.result.Index] = envelope.result
		case <-totalContext.Done():
			for _, task := range plan.Tasks {
				if _, exists := collected[task.Index]; exists {
					continue
				}
				status, reason := TaskStatusTimedOut, "total_timeout_exceeded"
				if parent.Err() != nil {
					status, reason = TaskStatusCancelled, "caller_cancelled"
				}
				collected[task.Index] = TaskExecution{Index: task.Index, TaskID: task.TaskID, Agent: task.Agent, Status: status, ReasonCode: reason, Output: emptyAgentOutput()}
			}
		}
	}
	result.TaskResults = make([]TaskExecution, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		result.TaskResults = append(result.TaskResults, collected[task.Index])
	}
	sort.SliceStable(result.TaskResults, func(i, j int) bool { return result.TaskResults[i].Index < result.TaskResults[j].Index })
	result.Usage = aggregateUsage(result.TaskResults)
	result.Status, result.ReasonCode = executionOutcome(result.TaskResults)
	result.DurationMS = time.Since(startedAt).Milliseconds()
	if parent.Err() != nil {
		result.Status, result.ReasonCode = ExecutionCancelled, "caller_cancelled"
		return result, parent.Err()
	}
	if errors.Is(totalContext.Err(), context.DeadlineExceeded) {
		result.Status, result.ReasonCode = ExecutionPartial, "total_timeout_exceeded"
	}
	return result, nil
}

func executeTask(parent context.Context, task PlannedTask, input ExecutionInput, runner AgentRunner) TaskExecution {
	startedAt := time.Now()
	result := TaskExecution{
		Index: task.Index, TaskID: task.TaskID, Agent: task.Agent, Status: TaskStatusFailed,
		ReasonCode: "runner_unavailable", Output: emptyAgentOutput(),
	}
	if runner == nil {
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	taskContext, cancel := context.WithTimeout(parent, time.Duration(task.Budget.TimeoutMS)*time.Millisecond)
	defer cancel()
	output, err := runner.Run(taskContext, task, input)
	result.DurationMS = time.Since(startedAt).Milliseconds()
	if err != nil {
		switch {
		case errors.Is(taskContext.Err(), context.DeadlineExceeded):
			result.Status, result.ReasonCode = TaskStatusTimedOut, "task_timeout_exceeded"
		case errors.Is(taskContext.Err(), context.Canceled):
			result.Status, result.ReasonCode = TaskStatusCancelled, "task_cancelled"
		default:
			result.Status, result.ReasonCode = TaskStatusFailed, "runner_failed"
		}
		return result
	}
	if err := validateAgentOutput(output, input.TenantID); err != nil {
		result.ReasonCode = "agent_output_invalid"
		return result
	}
	if exceedsTaskBudget(output, task.Budget) {
		result.Status, result.ReasonCode, result.Output = TaskStatusBudget, "task_budget_exceeded", usageOnlyOutput(output)
		return result
	}
	result.Status, result.ReasonCode, result.Output = TaskStatusSucceeded, "task_completed", output
	return result
}

func validateAgentOutput(output AgentOutput, tenantID string) error {
	if utf8.RuneCountInString(output.Summary) > maxAgentSummaryRunes || len(output.Claims) > maxAgentClaims || len(output.Evidence) > maxAgentEvidence || len(output.FollowUps) > maxAgentFollowUps {
		return errors.New("agent output exceeds bounds")
	}
	if output.Usage.InputTokens < 0 || output.Usage.OutputTokens < 0 || output.Usage.CostMicros < 0 || output.ToolCalls < 0 || output.Iterations < 0 {
		return errors.New("agent output usage is invalid")
	}
	for _, claim := range output.Claims {
		if strings.TrimSpace(claim.ID) == "" || strings.TrimSpace(claim.Statement) == "" || len(claim.EvidenceRefs) > 10 || claim.Confidence < 0 || claim.Confidence > 1 {
			return errors.New("agent claim is invalid")
		}
	}
	for _, evidence := range output.Evidence {
		if strings.TrimSpace(evidence.ID) == "" || strings.TrimSpace(evidence.SourceType) == "" || strings.TrimSpace(evidence.Summary) == "" || strings.TrimSpace(evidence.TenantID) != tenantID || evidence.Score < 0 || evidence.Score > 1 {
			return errors.New("agent evidence is invalid or outside tenant scope")
		}
	}
	return nil
}

func exceedsTaskBudget(output AgentOutput, budget TaskBudget) bool {
	return output.Iterations > budget.MaxIterations || output.ToolCalls > budget.MaxToolCalls ||
		output.Usage.InputTokens > budget.MaxInputTokens || output.Usage.OutputTokens > budget.MaxOutputTokens || output.Usage.CostMicros > budget.MaxCostMicros
}

func emptyAgentOutput() AgentOutput {
	return AgentOutput{Claims: []AgentClaim{}, Evidence: []SharedEvidence{}, FollowUps: []string{}}
}

func usageOnlyOutput(output AgentOutput) AgentOutput {
	return AgentOutput{
		Claims: []AgentClaim{}, Evidence: []SharedEvidence{}, FollowUps: []string{},
		Usage: output.Usage, ToolCalls: output.ToolCalls, Iterations: output.Iterations,
		OutputReason: "payload_dropped_after_budget_exceeded",
	}
}

func aggregateUsage(results []TaskExecution) BudgetUsage {
	usage := BudgetUsage{}
	for _, result := range results {
		usage.Agents++
		usage.ToolCalls += result.Output.ToolCalls
		usage.Iterations += result.Output.Iterations
		usage.InputTokens += result.Output.Usage.InputTokens
		usage.OutputTokens += result.Output.Usage.OutputTokens
		usage.CostMicros += result.Output.Usage.CostMicros
	}
	return usage
}

func executionOutcome(results []TaskExecution) (string, string) {
	succeeded, failed, budget := 0, 0, false
	for _, result := range results {
		switch result.Status {
		case TaskStatusSucceeded:
			succeeded++
		case TaskStatusBudget:
			budget = true
			failed++
		default:
			failed++
		}
	}
	switch {
	case budget:
		return ExecutionBudget, "agent_budget_exceeded"
	case succeeded == len(results):
		return ExecutionCompleted, "all_tasks_completed"
	case succeeded > 0 && failed > 0:
		return ExecutionPartial, "partial_results_available"
	default:
		return ExecutionFailed, "all_tasks_failed"
	}
}
