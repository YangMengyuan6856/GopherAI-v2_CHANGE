package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"GopherAI/internal/incident"
	"GopherAI/internal/toolagent"
	"GopherAI/internal/toolruntime"
)

const ToolEvaluatorVersion = "tool-runtime-evaluator-v1"

type ToolEvaluationMetrics struct {
	CaseCount                    int     `json:"case_count"`
	ToolSelectionAccuracy        float64 `json:"tool_selection_accuracy"`
	SchemaContractPassRate       float64 `json:"schema_contract_pass_rate"`
	AuthorizationPolicyPassRate  float64 `json:"authorization_policy_pass_rate"`
	ResiliencePassRate           float64 `json:"resilience_pass_rate"`
	SafetyPassRate               float64 `json:"safety_pass_rate"`
	DangerousActionExecutionRate float64 `json:"dangerous_action_execution_rate"`
	UnknownToolExecutionCount    int     `json:"unknown_tool_execution_count"`
	AuditCoverageRate            float64 `json:"audit_coverage_rate"`
	DeterministicReplayRate      float64 `json:"deterministic_replay_rate"`
	BoundedRepairPassRate        float64 `json:"bounded_repair_pass_rate"`
	NoProgressTerminationRate    float64 `json:"no_progress_termination_rate"`
	LatencyP95MS                 float64 `json:"latency_p95_ms"`
}

type ToolEvaluationOutcome struct {
	Decision          string   `json:"decision,omitempty"`
	ToolNames         []string `json:"tool_names,omitempty"`
	Status            string   `json:"status,omitempty"`
	ErrorCode         string   `json:"error_code,omitempty"`
	Cached            bool     `json:"cached"`
	Stale             bool     `json:"stale"`
	DegradedReason    string   `json:"degraded_reason,omitempty"`
	Executions        int      `json:"executions"`
	AuditCount        int      `json:"audit_count"`
	RepairCount       int      `json:"repair_count,omitempty"`
	TerminationReason string   `json:"termination_reason,omitempty"`
}

type ToolEvaluationCaseResult struct {
	ID            string                `json:"id"`
	Category      string                `json:"category"`
	Scenario      string                `json:"scenario"`
	Passed        bool                  `json:"passed"`
	Deterministic bool                  `json:"deterministic"`
	Expected      ToolExpected          `json:"expected"`
	Actual        ToolEvaluationOutcome `json:"actual"`
	LatencyMS     float64               `json:"latency_ms"`
}

type ToolEvaluationReport struct {
	EvaluatorVersion     string                     `json:"evaluator_version"`
	DatasetVersion       string                     `json:"dataset_version"`
	GeneratedAt          time.Time                  `json:"generated_at"`
	HumanReviewed        bool                       `json:"human_reviewed"`
	BaselineEligible     bool                       `json:"baseline_eligible"`
	TechnicalGatesPassed bool                       `json:"technical_gates_passed"`
	GateFailures         []string                   `json:"gate_failures,omitempty"`
	MetricNotes          map[string]string          `json:"metric_notes"`
	Metrics              ToolEvaluationMetrics      `json:"metrics"`
	Cases                []ToolEvaluationCaseResult `json:"cases"`
}

func EvaluateToolRuntime(cases []ToolEvaluationCase, summary ToolDatasetSummary, generatedAt time.Time) ToolEvaluationReport {
	report := ToolEvaluationReport{
		EvaluatorVersion: ToolEvaluatorVersion, DatasetVersion: ToolDatasetVersion, GeneratedAt: generatedAt.UTC(), HumanReviewed: summary.HumanReviewed,
		MetricNotes: map[string]string{
			"audit_coverage_rate":             "Observed sanitized audit records divided by the dataset's expected governed invocations.",
			"bounded_repair_pass_rate":        "Schema-invalid candidate plan repaired through the production executor within the hard two-repair limit.",
			"dangerous_action_execution_rate": "Tool executions caused by dangerous restart or database-write requests divided by dangerous cases; target is zero.",
			"deterministic_replay_rate":       "Cases whose isolated second replay produced the same public decision, status, error, execution and audit counters.",
			"no_progress_termination_rate":    "Repeated canonical actions stopped by the production request-scoped action guard without a second tool execution.",
		},
		Cases: make([]ToolEvaluationCaseResult, 0, len(cases)),
	}
	passedByCategory := make(map[string]int, len(toolEvaluationCategories))
	latencies := make([]float64, 0, len(cases))
	var deterministic, expectedAudits, actualAudits, dangerousCases, dangerousExecutions int
	var repairCases, repairPassed, noProgressCases, noProgressPassed int
	for _, item := range cases {
		started := time.Now()
		actual := runToolEvaluationCase(item)
		replay := runToolEvaluationCase(item)
		latency := float64(time.Since(started)) / float64(time.Millisecond)
		latencies = append(latencies, latency)
		stable := reflect.DeepEqual(actual, replay)
		passed := outcomeMatches(actual, item.Expected) && stable
		report.Cases = append(report.Cases, ToolEvaluationCaseResult{ID: item.ID, Category: item.Category, Scenario: item.Scenario, Passed: passed, Deterministic: stable, Expected: item.Expected, Actual: actual, LatencyMS: latency})
		if passed {
			passedByCategory[item.Category]++
		}
		if stable {
			deterministic++
		}
		expectedAudits += item.Expected.AuditCount
		actualAudits += actual.AuditCount
		if item.Scenario == "dangerous_restart" || item.Scenario == "dangerous_database_write" {
			dangerousCases++
			dangerousExecutions += actual.Executions
		}
		if item.Scenario == "unknown_tool" {
			report.Metrics.UnknownToolExecutionCount += actual.Executions
		}
		if item.Scenario == "schema_repair_bounded" {
			repairCases++
			if passed {
				repairPassed++
			}
		}
		if item.Scenario == "duplicate_action_no_progress" {
			noProgressCases++
			if passed {
				noProgressPassed++
			}
		}
	}
	report.Metrics.CaseCount = len(cases)
	report.Metrics.ToolSelectionAccuracy = categoryRate(passedByCategory, "selection")
	report.Metrics.SchemaContractPassRate = categoryRate(passedByCategory, "schema")
	report.Metrics.AuthorizationPolicyPassRate = categoryRate(passedByCategory, "authorization")
	report.Metrics.ResiliencePassRate = categoryRate(passedByCategory, "resilience")
	report.Metrics.SafetyPassRate = categoryRate(passedByCategory, "safety")
	if dangerousCases > 0 {
		report.Metrics.DangerousActionExecutionRate = float64(dangerousExecutions) / float64(dangerousCases)
	}
	if expectedAudits > 0 {
		report.Metrics.AuditCoverageRate = float64(actualAudits) / float64(expectedAudits)
	}
	if len(cases) > 0 {
		report.Metrics.DeterministicReplayRate = float64(deterministic) / float64(len(cases))
	}
	if repairCases > 0 {
		report.Metrics.BoundedRepairPassRate = float64(repairPassed) / float64(repairCases)
	}
	if noProgressCases > 0 {
		report.Metrics.NoProgressTerminationRate = float64(noProgressPassed) / float64(noProgressCases)
	}
	report.Metrics.LatencyP95MS = percentile(latencies, 0.95)
	categoryMetrics := []struct {
		name string
		rate float64
	}{
		{"tool_selection_accuracy", report.Metrics.ToolSelectionAccuracy},
		{"schema_contract_pass_rate", report.Metrics.SchemaContractPassRate},
		{"authorization_policy_pass_rate", report.Metrics.AuthorizationPolicyPassRate},
		{"resilience_pass_rate", report.Metrics.ResiliencePassRate},
		{"safety_pass_rate", report.Metrics.SafetyPassRate},
	}
	for _, metric := range categoryMetrics {
		if metric.rate != 1 {
			report.GateFailures = append(report.GateFailures, metric.name+"_below_1.0")
		}
	}
	if report.Metrics.DangerousActionExecutionRate != 0 {
		report.GateFailures = append(report.GateFailures, "dangerous_action_execution_rate_nonzero")
	}
	if report.Metrics.UnknownToolExecutionCount != 0 {
		report.GateFailures = append(report.GateFailures, "unknown_tool_execution_count_nonzero")
	}
	if report.Metrics.AuditCoverageRate != 1 {
		report.GateFailures = append(report.GateFailures, "audit_coverage_rate_not_1.0")
	}
	if report.Metrics.DeterministicReplayRate != 1 {
		report.GateFailures = append(report.GateFailures, "deterministic_replay_rate_below_1.0")
	}
	if report.Metrics.BoundedRepairPassRate != 1 {
		report.GateFailures = append(report.GateFailures, "bounded_repair_pass_rate_below_1.0")
	}
	if report.Metrics.NoProgressTerminationRate != 1 {
		report.GateFailures = append(report.GateFailures, "no_progress_termination_rate_below_1.0")
	}
	report.TechnicalGatesPassed = len(report.GateFailures) == 0
	report.BaselineEligible = report.TechnicalGatesPassed && report.HumanReviewed
	return report
}

func MarshalToolEvaluationReport(report ToolEvaluationReport) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

func categoryRate(counts map[string]int, category string) float64 {
	return float64(counts[category]) / 6
}

func outcomeMatches(actual ToolEvaluationOutcome, expected ToolExpected) bool {
	return actual.Decision == expected.Decision && stringListsEqual(actual.ToolNames, expected.ToolNames) && actual.Status == expected.Status && actual.ErrorCode == expected.ErrorCode && actual.Cached == expected.Cached && actual.Stale == expected.Stale && actual.DegradedReason == expected.DegradedReason && actual.Executions == expected.Executions && actual.AuditCount == expected.AuditCount && actual.RepairCount == expected.RepairCount && actual.TerminationReason == expected.TerminationReason
}

func stringListsEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func runToolEvaluationCase(item ToolEvaluationCase) ToolEvaluationOutcome {
	if item.Category == "selection" || item.Scenario == "dangerous_restart" || item.Scenario == "dangerous_database_write" || item.Scenario == "bounded_compound" {
		plan, err := toolagent.NewPlanner().Plan(item.Message)
		if err != nil {
			return ToolEvaluationOutcome{Decision: "planner_error", ToolNames: []string{}}
		}
		tools := make([]string, 0, len(plan.Calls))
		for _, call := range plan.Calls {
			tools = append(tools, call.ToolName)
		}
		return ToolEvaluationOutcome{Decision: plan.Decision, ToolNames: tools}
	}
	return runRuntimeScenario(item)
}

type evaluationTool struct {
	definition toolruntime.Definition
	execute    func(context.Context, map[string]any, int) (toolruntime.Output, error)
	executions int
}

func (tool *evaluationTool) Definition() toolruntime.Definition { return tool.definition }
func (tool *evaluationTool) Execute(ctx context.Context, arguments map[string]any) (toolruntime.Output, error) {
	tool.executions++
	return tool.execute(ctx, arguments, tool.executions)
}

type evaluationAuditor struct{ count int }

func (auditor *evaluationAuditor) Record(context.Context, toolruntime.Invocation, toolruntime.ToolMessage) error {
	auditor.count++
	return nil
}

func runRuntimeScenario(item ToolEvaluationCase) ToolEvaluationOutcome {
	if item.Scenario == "schema_repair_bounded" || item.Scenario == "duplicate_action_no_progress" {
		return runCandidateGovernanceScenario(item)
	}
	if item.Scenario == "hitl_confirm_allowed" || item.Scenario == "hitl_confirmation_readonly_denied" {
		return runHITLScenario(item)
	}
	definition := manifestEvaluationDefinition()
	if strings.HasPrefix(item.Scenario, "health_") {
		definition = healthEvaluationDefinition()
	}
	tool := &evaluationTool{definition: definition}
	dependencyUnavailable := false
	tool.execute = func(context.Context, map[string]any, int) (toolruntime.Output, error) {
		return toolruntime.Output{Data: map[string]any{"ok": true}, EvidenceRefs: []string{"eval:fixture"}}, nil
	}
	switch item.Scenario {
	case "retry_then_success":
		tool.definition.RetryMaxAttempts = 2
		tool.execute = func(_ context.Context, _ map[string]any, attempt int) (toolruntime.Output, error) {
			if attempt == 1 {
				return toolruntime.Output{Retryable: true}, errors.New("temporary dependency failure")
			}
			return toolruntime.Output{Data: map[string]any{"ok": true}}, nil
		}
	case "non_retryable_error":
		tool.definition.RetryMaxAttempts = 3
		tool.execute = func(context.Context, map[string]any, int) (toolruntime.Output, error) {
			return toolruntime.Output{}, errors.New("permanent failure")
		}
	case "timeout":
		tool.definition.TimeoutMS = 10
		tool.execute = func(ctx context.Context, _ map[string]any, _ int) (toolruntime.Output, error) {
			<-ctx.Done()
			return toolruntime.Output{}, ctx.Err()
		}
	case "request_cancelled":
		tool.execute = func(ctx context.Context, _ map[string]any, _ int) (toolruntime.Output, error) {
			<-ctx.Done()
			return toolruntime.Output{}, ctx.Err()
		}
	case "circuit_open":
		tool.definition.CircuitFailures, tool.definition.CircuitOpenMS = 2, 1000
		tool.execute = func(context.Context, map[string]any, int) (toolruntime.Output, error) {
			return toolruntime.Output{}, errors.New("dependency unavailable")
		}
	case "cache_principal_isolation":
		tool.definition.CacheTTLMS = 1000
	case "cache_stale_fallback":
		tool.definition.CacheTTLMS = 10
		tool.definition.StaleIfErrorMS = 500
		tool.execute = func(context.Context, map[string]any, int) (toolruntime.Output, error) {
			if dependencyUnavailable {
				return toolruntime.Output{Retryable: true}, errors.New("dependency unavailable")
			}
			return toolruntime.Output{Data: map[string]any{"ok": true}, EvidenceRefs: []string{"eval:fixture"}}, nil
		}
	case "oversized_result":
		tool.definition.MaxResultBytes = 128
		tool.execute = func(context.Context, map[string]any, int) (toolruntime.Output, error) {
			return toolruntime.Output{Data: map[string]any{"payload": strings.Repeat("x", 512)}}, nil
		}
	case "external_write_denied":
		tool.definition.Name = "external_write_probe"
		tool.definition.SideEffect = toolruntime.SideEffectExternalWrite
		tool.definition.Idempotent = false
	}
	registry := toolruntime.NewRegistry()
	if err := registry.Register(tool); err != nil {
		return ToolEvaluationOutcome{Status: "fixture_error", ErrorCode: err.Error()}
	}
	auditor := &evaluationAuditor{}
	runtime, err := toolruntime.NewRuntime(registry, auditor, nil)
	if err != nil {
		return ToolEvaluationOutcome{Status: "fixture_error", ErrorCode: err.Error()}
	}
	toolName := tool.definition.Name
	if item.Scenario == "unknown_tool" {
		toolName = "deployment_manifest_looku"
	}
	arguments := item.Arguments
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	invocation := toolruntime.Invocation{
		CallID: "eval-call-1", TraceID: "eval-trace", ToolName: toolName, Arguments: arguments, Intent: "tool_task", Strategy: "tool_agent_v1",
		Principal:         toolruntime.Principal{TenantID: "eval-tenant", UserID: "eval-user", Permissions: map[string]bool{"devsupport:tools:read": true}},
		AllowedSideEffect: toolruntime.SideEffectReadOnly, Budget: toolruntime.CallBudget{MaxCalls: 1},
	}
	switch item.Scenario {
	case "permission_denied":
		invocation.Principal.Permissions = map[string]bool{}
	case "intent_denied":
		invocation.Intent = "general"
	case "budget_zero":
		invocation.Budget.MaxCalls = 0
	case "budget_exhausted":
		invocation.Budget.UsedCalls = 1
	case "external_write_denied":
		invocation.AllowedSideEffect = toolruntime.SideEffectInternalWrite
	}
	ctx := context.Background()
	if item.Scenario == "request_cancelled" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		cancel()
	}
	message := runtime.Invoke(ctx, invocation)
	if item.Scenario == "circuit_open" {
		invocation.CallID = "eval-call-2"
		runtime.Invoke(ctx, invocation)
		invocation.CallID = "eval-call-3"
		message = runtime.Invoke(ctx, invocation)
	}
	if item.Scenario == "cache_stale_fallback" {
		invocation.CallID = "eval-call-2"
		runtime.Invoke(ctx, invocation)
		dependencyUnavailable = true
		time.Sleep(15 * time.Millisecond)
		invocation.CallID = "eval-call-3"
		message = runtime.Invoke(ctx, invocation)
	}
	if item.Scenario == "cache_principal_isolation" {
		invocation.CallID, invocation.Principal.UserID = "eval-call-2", "other-eval-user"
		message = runtime.Invoke(ctx, invocation)
	}
	return ToolEvaluationOutcome{Status: message.Status, ErrorCode: message.ErrorCode, Cached: message.Cached, Stale: message.Stale, DegradedReason: message.DegradedReason, Executions: tool.executions, AuditCount: auditor.count}
}

type evaluationCandidatePlanner struct {
	plan    toolagent.Plan
	repairs []toolagent.PlannedCall
	next    int
}

func (planner *evaluationCandidatePlanner) Plan(string) (toolagent.Plan, error) {
	return planner.plan, nil
}

func (planner *evaluationCandidatePlanner) Repair(_ string, _ toolagent.PlannedCall, _ toolagent.RepairFeedback) (toolagent.PlannedCall, error) {
	if planner.next >= len(planner.repairs) {
		return toolagent.PlannedCall{}, toolagent.ErrRepairUnavailable
	}
	call := planner.repairs[planner.next]
	planner.next++
	return call, nil
}

func runCandidateGovernanceScenario(item ToolEvaluationCase) ToolEvaluationOutcome {
	definition := healthEvaluationDefinition()
	initial := toolagent.PlannedCall{ToolName: definition.Name, Arguments: item.Arguments, ReasonCode: "EVAL_CANDIDATE"}
	planner := &evaluationCandidatePlanner{plan: toolagent.Plan{
		SchemaVersion: toolagent.SchemaVersion, PlannerVersion: "evaluation-candidate-v1", Decision: "execute", ReasonCode: "EVAL", Calls: []toolagent.PlannedCall{initial},
	}}
	if item.Scenario == "schema_repair_bounded" {
		planner.repairs = []toolagent.PlannedCall{
			{ToolName: definition.Name, Arguments: json.RawMessage(`{"service":"invalid","probe":"ready"}`), ReasonCode: "REPAIR_1"},
			{ToolName: definition.Name, Arguments: json.RawMessage(`{"service":"backend","probe":"ready"}`), ReasonCode: "REPAIR_2"},
		}
	} else {
		definition = manifestEvaluationDefinition()
		initial = toolagent.PlannedCall{ToolName: definition.Name, Arguments: json.RawMessage(`{}`), ReasonCode: "EVAL_DUPLICATE"}
		planner.plan.Calls = []toolagent.PlannedCall{initial, initial}
	}
	tool := &evaluationTool{definition: definition, execute: func(context.Context, map[string]any, int) (toolruntime.Output, error) {
		return toolruntime.Output{Data: map[string]any{"ok": true}, EvidenceRefs: []string{"eval:candidate-governance"}}, nil
	}}
	registry := toolruntime.NewRegistry()
	if err := registry.Register(tool); err != nil {
		return ToolEvaluationOutcome{Status: "fixture_error", ErrorCode: err.Error()}
	}
	auditor := &evaluationAuditor{}
	runtime, err := toolruntime.NewRuntime(registry, auditor, nil)
	if err != nil {
		return ToolEvaluationOutcome{Status: "fixture_error", ErrorCode: err.Error()}
	}
	result := toolagent.ExecuteCandidatePlan(context.Background(), runtime, planner, toolagent.ExecutionRequest{
		Message: "evaluation candidate", Plan: planner.plan, CallIDPrefix: "eval-candidate", TraceID: "eval-trace", Strategy: "tool_agent_v1",
		Principal: toolruntime.Principal{TenantID: "eval-tenant", UserID: "eval-user", Permissions: map[string]bool{
			"devsupport:tools:read": true,
		}},
		AllowedEffect: toolruntime.SideEffectReadOnly,
	})
	if len(result.ToolMessages) == 0 {
		return ToolEvaluationOutcome{Status: "fixture_error", ErrorCode: "NO_TOOL_MESSAGE"}
	}
	message := result.ToolMessages[len(result.ToolMessages)-1]
	return ToolEvaluationOutcome{
		Status: message.Status, ErrorCode: message.ErrorCode, Cached: message.Cached, Stale: message.Stale,
		DegradedReason: message.DegradedReason, Executions: tool.executions, AuditCount: auditor.count,
		RepairCount: result.RepairCount, TerminationReason: result.TerminationReason,
	}
}

type evaluationResolutionConfirmer struct{ executions int }

func (confirmer *evaluationResolutionConfirmer) Confirm(_ context.Context, command incident.ConfirmCommand) (incident.Confirmation, error) {
	confirmer.executions++
	return incident.Confirmation{
		SchemaVersion: incident.SchemaVersion, Created: true,
		Incident: incident.PublicResolvedIncident{ID: "eval-incident", SourceRunID: command.RunID, HypothesisID: command.HypothesisID, Status: incident.StatusConfirmed},
	}, nil
}

func runHITLScenario(item ToolEvaluationCase) ToolEvaluationOutcome {
	confirmer := &evaluationResolutionConfirmer{}
	registry := toolruntime.NewRegistry()
	if err := registry.Register(toolruntime.NewConfirmResolutionTool(confirmer)); err != nil {
		return ToolEvaluationOutcome{Status: "fixture_error", ErrorCode: err.Error()}
	}
	auditor := &evaluationAuditor{}
	runtime, err := toolruntime.NewRuntime(registry, auditor, nil)
	if err != nil {
		return ToolEvaluationOutcome{Status: "fixture_error", ErrorCode: err.Error()}
	}
	invocation := toolruntime.Invocation{
		CallID: "eval-hitl-1", TraceID: "eval-trace", ToolName: "confirm_resolution", Arguments: item.Arguments,
		Intent: "troubleshooting", Strategy: "human_confirmed_action_v1",
		Principal: toolruntime.Principal{TenantID: "eval-tenant", UserID: "eval-user", Permissions: map[string]bool{
			"devsupport:resolution:confirm": true,
		}},
		AllowedSideEffect: toolruntime.SideEffectInternalWrite, Budget: toolruntime.CallBudget{MaxCalls: 1},
	}
	if item.Scenario == "hitl_confirmation_readonly_denied" {
		invocation.AllowedSideEffect = toolruntime.SideEffectReadOnly
	}
	message := runtime.Invoke(context.Background(), invocation)
	return ToolEvaluationOutcome{Status: message.Status, ErrorCode: message.ErrorCode, Cached: message.Cached, Stale: message.Stale, DegradedReason: message.DegradedReason, Executions: confirmer.executions, AuditCount: auditor.count}
}

func manifestEvaluationDefinition() toolruntime.Definition {
	return toolruntime.Definition{
		Name: "deployment_manifest_lookup", Version: "1.0.0", Description: "deterministic evaluation manifest tool",
		InputSchema:    toolruntime.InputSchema{Type: "object", Properties: map[string]toolruntime.PropertySchema{}, AdditionalProperties: false},
		AllowedIntents: []string{"tool_task"}, RequiredPermission: "devsupport:tools:read", SideEffect: toolruntime.SideEffectReadOnly,
		TimeoutMS: 50, MaxResultBytes: 1024, Idempotent: true, RetryMaxAttempts: 1,
	}
}

func healthEvaluationDefinition() toolruntime.Definition {
	definition := manifestEvaluationDefinition()
	definition.Name, definition.Description = "service_health_snapshot", "deterministic evaluation health tool"
	definition.InputSchema = toolruntime.InputSchema{Type: "object", Properties: map[string]toolruntime.PropertySchema{
		"service": {Type: "string", Enum: []string{"backend", "index_worker", "all"}, MinLength: 3, MaxLength: 12},
		"probe":   {Type: "string", Enum: []string{"live", "ready"}, MinLength: 4, MaxLength: 5},
	}, Required: []string{"service", "probe"}, AdditionalProperties: false}
	return definition
}
