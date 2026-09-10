package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"GopherAI/internal/contract"
	"GopherAI/internal/diagnostic"
	"GopherAI/internal/toolruntime"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

const DynamicMaxRounds = 5
const DynamicMaxDelegations = 4
const DynamicTimeout = 180 * time.Second
const dynamicContextBytes = 64000
const dynamicCumulativeBytes = 200000

type CollaborationModel interface {
	Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error)
}

type Delegation struct {
	Agent       string   `json:"agent"`
	Objective   string   `json:"objective"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type SupervisorDecision struct {
	Action    string       `json:"action"`
	Update    string       `json:"update"`
	Tasks     []Delegation `json:"tasks"`
	Questions []string     `json:"questions"`
}

type DynamicStep struct {
	Round      int                 `json:"round"`
	Decision   *SupervisorDecision `json:"decision,omitempty"`
	Validation string              `json:"validation"`
	ModelMS    int64               `json:"model_ms"`
	Tasks      []TaskExecution     `json:"tasks"`
}

type DynamicTrace struct {
	Version         string              `json:"version"`
	Model           string              `json:"model"`
	PromptSHA256    string              `json:"prompt_sha256"`
	Steps           []DynamicStep       `json:"steps"`
	StopReason      string              `json:"stop_reason"`
	Questions       []string            `json:"questions"`
	SupervisorCalls int                 `json:"supervisor_calls"`
	Delegations     int                 `json:"delegations"`
	Usage           contract.ModelUsage `json:"usage"`
	InputBytes      int                 `json:"supervisor_input_bytes"`
	AuditStorage    string              `json:"audit_storage"`
}

type DynamicSupervisor struct {
	model     CollaborationModel
	modelName string
	runners   map[string]AgentRunner
	auditor   toolruntime.Auditor
	timeout   time.Duration
}

func NewDynamicSupervisor(m CollaborationModel, modelName string, runners map[string]AgentRunner, auditor toolruntime.Auditor) (*DynamicSupervisor, error) {
	if m == nil || runners[KnowledgeAgentRole] == nil || runners[DiagnosticAgentRole] == nil || len(runners) != 2 {
		return nil, errors.New("dynamic supervisor requires a model and exactly two registered agents")
	}
	return &DynamicSupervisor{model: m, modelName: modelName, runners: map[string]AgentRunner{KnowledgeAgentRole: runners[KnowledgeAgentRole], DiagnosticAgentRole: runners[DiagnosticAgentRole]}, auditor: auditor, timeout: DynamicTimeout}, nil
}

func (s *DynamicSupervisor) Run(parent context.Context, input ExecutionInput) (CollaborationRun, error) {
	started := time.Now()
	extracted, err := (diagnostic.Extractor{}).Extract(input.Message)
	if err != nil || strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.UserID) == "" {
		return CollaborationRun{}, errors.New("invalid collaboration input")
	}
	input.Message, input.SharedEvidence = extracted.SanitizedExcerpt, nil
	if input.TraceID == "" {
		input.TraceID = uuid.NewString()
	}
	digest := sha256.Sum256([]byte(supervisorPrompt + delegatedDiagnosticPrompt))
	trace := &DynamicTrace{Version: DynamicVersion, Model: s.modelName, PromptSHA256: hex.EncodeToString(digest[:]), Steps: []DynamicStep{}, Questions: []string{}, StopReason: "round_budget", AuditStorage: "response_trace_only"}
	if s.auditor != nil {
		trace.AuditStorage = "mysql_control_audit"
	}
	out := CollaborationRun{SchemaVersion: "collaboration-dynamic-shadow-v1", Mode: "shadow_only", TraceID: input.TraceID, Status: "insufficient", Orchestration: trace,
		Plan: CollaborationPlan{SchemaVersion: PlanSchemaVersion, PlannerVersion: DynamicVersion, Mode: "shadow_only", Decision: "dynamic", Strategy: DynamicVersion, ReasonCode: "model_directed", Tasks: []PlannedTask{}, SanitizationRedactions: extracted.RedactionCount,
			Budget:      PlanBudget{MaxAgents: 2, MaxIterations: DynamicMaxRounds, MaxToolCalls: DynamicMaxDelegations, TotalTimeoutMS: DynamicTimeout.Milliseconds()},
			Limitations: []string{"最多两个固定角色，每轮最多并行两个，共四次委派；只读，不切换正式聊天。", "诊断只分析用户报告和文档证据，不连接主机；引用有效不证明根因正确。"}}}
	ctx, cancel := context.WithTimeout(parent, s.timeout)
	defer cancel()
	bank := map[string]SharedEvidence{}
	allTasks := []TaskExecution{}
	seenActions := map[string]bool{}
	invalidCount, noProgress := 0, 0
	var feedback any = map[string]any{"original_request": input.Message}
	for round := 1; round <= DynamicMaxRounds; round++ {
		if ctx.Err() != nil {
			trace.StopReason = "cancelled_or_timeout"
			break
		}
		payload, _ := json.Marshal(map[string]any{"original_request": input.Message, "round": round, "remaining_delegations": DynamicMaxDelegations - trace.Delegations, "remaining_supervisor_calls": DynamicMaxRounds - round, "observation": feedback})
		if len(payload) > dynamicContextBytes || trace.InputBytes+len(payload) > dynamicCumulativeBytes {
			trace.StopReason = "context_budget"
			break
		}
		trace.InputBytes += len(payload)
		callCtx, stop := context.WithTimeout(ctx, 30*time.Second)
		callStart := time.Now()
		trace.SupervisorCalls++
		response, callErr := s.model.Generate(callCtx, []*schema.Message{schema.SystemMessage(supervisorPrompt), schema.UserMessage(string(payload))})
		stop()
		trace.Usage = addModelUsage(trace.Usage, messageUsage(response))
		step := DynamicStep{Round: round, ModelMS: time.Since(callStart).Milliseconds(), Tasks: []TaskExecution{}}
		if callErr != nil {
			step.Validation = "model_error_or_timeout"
			trace.Steps = append(trace.Steps, step)
			trace.StopReason = step.Validation
			break
		}
		decision, reason := parseSupervisor(response, bank, DynamicMaxDelegations-trace.Delegations)
		step.Validation = reason
		if reason != "accepted" {
			trace.Steps = append(trace.Steps, step)
			invalidCount++
			if invalidCount >= 2 {
				trace.StopReason = "invalid_model_output"
				break
			}
			feedback = map[string]any{"validation_error": reason, "prior_results": allTasks, "evidence": sortedEvidence(bank)}
			continue
		}
		step.Decision = &decision
		if err := s.audit(ctx, input, fmt.Sprintf("supervisor-%d", round), "collaboration_supervisor", decision, "accepted", ""); err != nil {
			step.Validation = "audit_unavailable"
			trace.Steps = append(trace.Steps, step)
			trace.StopReason = step.Validation
			trace.AuditStorage = "audit_failed"
			break
		}
		if decision.Action == "finish" {
			trace.Questions = decision.Questions
			trace.StopReason = "model_finished"
			trace.Steps = append(trace.Steps, step)
			break
		}
		// Validate the whole batch before executing any child; repeating a canonical
		// objective with identical handed-off evidence cannot spend another budget slot.
		repeated := false
		for _, task := range decision.Tasks {
			if seenActions[delegationKey(task)] {
				repeated = true
			}
		}
		if repeated {
			step.Validation = "repeated_delegation"
			trace.Steps = append(trace.Steps, step)
			trace.StopReason = step.Validation
			break
		}
		results := make(chan TaskExecution, len(decision.Tasks))
		pending := map[int]PlannedTask{}
		before := len(bank)
		for _, delegation := range decision.Tasks {
			seenActions[delegationKey(delegation)] = true
			trace.Delegations++
			index := trace.Delegations
			task := PlannedTask{Index: index, TaskID: fmt.Sprintf("d%d", index), Agent: delegation.Agent, Objective: delegation.Objective, InputReference: "original_request_and_selected_evidence", OutputContract: "evidence-bound-agent-output-v1", Budget: TaskBudget{TimeoutMS: 60000, MaxIterations: 3, MaxToolCalls: 2, MaxInputTokens: 24000, MaxOutputTokens: 4000, MaxCostMicros: 2000000}}
			out.Plan.Tasks = append(out.Plan.Tasks, task)
			pending[index] = task
			childInput := input
			childInput.SharedEvidence = []SharedEvidence{}
			for _, id := range delegation.EvidenceIDs {
				childInput.SharedEvidence = append(childInput.SharedEvidence, bank[id])
			}
			runner := s.runners[delegation.Agent]
			go func(t PlannedTask, in ExecutionInput, r AgentRunner) { results <- executeTask(ctx, t, in, r) }(task, childInput, runner)
		}
		out.Executed = true
		for len(pending) > 0 {
			select {
			case task := <-results:
				delete(pending, task.Index)
				namespaceTask(&task, bank)
				if err := s.audit(ctx, input, task.TaskID, "delegate_"+task.Agent, task, task.Status, task.ReasonCode); err != nil {
					task.Status = TaskStatusFailed
					task.ReasonCode = "audit_unavailable"
					task.Output = usageOnlyOutput(task.Output)
					trace.AuditStorage = "audit_failed"
				}
				step.Tasks = append(step.Tasks, task)
			case <-ctx.Done():
				for _, t := range pending {
					task := TaskExecution{Index: t.Index, TaskID: t.TaskID, Agent: t.Agent, Status: TaskStatusTimedOut, ReasonCode: "total_timeout_exceeded", Output: emptyAgentOutput()}
					if parent.Err() != nil {
						task.Status = TaskStatusCancelled
						task.ReasonCode = "caller_cancelled"
					}
					if s.audit(ctx, input, task.TaskID, "delegate_"+task.Agent, task, task.Status, task.ReasonCode) != nil {
						trace.AuditStorage = "audit_failed"
					}
					step.Tasks = append(step.Tasks, task)
				}
				pending = map[int]PlannedTask{}
			}
		}
		sort.Slice(step.Tasks, func(i, j int) bool { return step.Tasks[i].Index < step.Tasks[j].Index })
		for _, task := range step.Tasks {
			allTasks = append(allTasks, task)
			trace.Usage = addModelUsage(trace.Usage, task.Output.Usage)
			if task.Status == TaskStatusSucceeded {
				// Only citations admitted by the existing merger enter the shared bank.
				checked, _ := NewEvidenceAwareSynthesizer().Synthesize(context.WithoutCancel(ctx), ExecutionResult{SchemaVersion: ExecutionSchemaVersion, Mode: "shadow_only", TaskResults: []TaskExecution{task}}, input.TenantID)
				for _, e := range checked.Evidence {
					bank[e.ID] = e
				}
			}
		}
		trace.Steps = append(trace.Steps, step)
		if trace.AuditStorage == "audit_failed" {
			trace.StopReason = "audit_unavailable"
			break
		}
		if len(bank) == before {
			noProgress++
		} else {
			noProgress = 0
		}
		if noProgress >= 2 {
			trace.StopReason = "no_new_evidence"
			break
		}
		feedback = map[string]any{"prior_results": allTasks, "evidence": sortedEvidence(bank), "last_decision": decision}
	}
	out.Execution = &ExecutionResult{SchemaVersion: ExecutionSchemaVersion, ExecutorVersion: DynamicVersion, Mode: "shadow_only", TaskResults: allTasks, Usage: aggregateUsage(allTasks), DurationMS: time.Since(started).Milliseconds(), InputRedactions: extracted.RedactionCount}
	out.Execution.Status, out.Execution.ReasonCode = executionOutcome(allTasks)
	// Retain cited results across rounds, not just the last task of each role.
	synthesis, _ := NewEvidenceAwareSynthesizer().Synthesize(context.WithoutCancel(ctx), *out.Execution, input.TenantID)
	synthesis.FallbackStrategy = "" // No hidden execution of the legacy fallback.
	out.Status = synthesis.Status
	for _, t := range allTasks {
		if t.Status != TaskStatusSucceeded && out.Status == SynthesisComplete {
			out.Status = SynthesisPartial
		}
	}
	if trace.StopReason != "model_finished" && out.Status == SynthesisComplete {
		out.Status = SynthesisPartial
	}
	if parent.Err() != nil {
		out.Status = "cancelled"
	}
	if !out.Executed && trace.StopReason != "model_finished" {
		out.Status = "failed"
	}
	synthesis.Status = out.Status
	synthesis.UnifiedAnswer = dynamicAnswer(synthesis)
	out.Synthesis = &synthesis
	out.ReasonCode = trace.StopReason
	return out, nil
}

func parseSupervisor(response *schema.Message, bank map[string]SharedEvidence, remaining int) (SupervisorDecision, string) {
	var d SupervisorDecision
	if response == nil || len(response.Content) > 12000 || strictJSON(response.Content, &d) != nil {
		return d, "invalid_json_schema"
	}
	if utf8.RuneCountInString(d.Update) > 500 || strings.TrimSpace(d.Update) == "" || len(d.Questions) > 5 {
		return d, "invalid_update_or_questions"
	}
	d.Update = cleanDynamicText(d.Update, 500)
	for i, q := range d.Questions {
		if utf8.RuneCountInString(q) > 500 {
			return d, "question_too_long"
		}
		d.Questions[i] = cleanDynamicText(q, 500)
	}
	if d.Action == "finish" && len(d.Tasks) == 0 {
		return d, "accepted"
	}
	if d.Action != "delegate" || len(d.Tasks) < 1 || len(d.Tasks) > 2 || len(d.Tasks) > remaining {
		return d, "invalid_delegation_or_budget"
	}
	roles := map[string]bool{}
	for i, t := range d.Tasks {
		if (t.Agent != KnowledgeAgentRole && t.Agent != DiagnosticAgentRole) || roles[t.Agent] {
			return d, "agent_not_allowed_or_duplicate"
		}
		roles[t.Agent] = true
		if strings.TrimSpace(t.Objective) == "" || utf8.RuneCountInString(t.Objective) > 1000 || len(t.EvidenceIDs) > 8 {
			return d, "invalid_task_bounds"
		}
		d.Tasks[i].Objective = cleanDynamicText(t.Objective, 1000)
		if d.Tasks[i].Objective == "" {
			return d, "empty_sanitized_task"
		}
		for _, id := range t.EvidenceIDs {
			if _, ok := bank[id]; !ok {
				return d, "unknown_handoff_evidence"
			}
		}
	}
	return d, "accepted"
}

func strictJSON(raw string, target any) error {
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func cleanDynamicText(s string, n int) string { v, _, _ := diagnostic.SanitizeFreeText(s, n); return v }
func delegationKey(t Delegation) string {
	ids := append([]string{}, t.EvidenceIDs...)
	sort.Strings(ids)
	return t.Agent + ":" + strings.Join(strings.Fields(strings.ToLower(t.Objective)), "") + ":" + strings.Join(ids, ",")
}
func sortedEvidence(bank map[string]SharedEvidence) []SharedEvidence {
	keys := make([]string, 0, len(bank))
	for id := range bank {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	out := []SharedEvidence{}
	for _, id := range keys {
		out = append(out, bank[id])
	}
	return out
}
func messageUsage(m *schema.Message) contract.ModelUsage {
	if m == nil || m.ResponseMeta == nil || m.ResponseMeta.Usage == nil {
		return contract.ModelUsage{}
	}
	return contract.ModelUsage{InputTokens: m.ResponseMeta.Usage.PromptTokens, OutputTokens: m.ResponseMeta.Usage.CompletionTokens}
}
func addModelUsage(a, b contract.ModelUsage) contract.ModelUsage {
	return contract.ModelUsage{InputTokens: a.InputTokens + b.InputTokens, OutputTokens: a.OutputTokens + b.OutputTokens, CostMicros: a.CostMicros + b.CostMicros}
}

func namespaceTask(t *TaskExecution, bank map[string]SharedEvidence) {
	if t.Status != TaskStatusSucceeded && t.Status != TaskStatusInsufficient {
		return
	}
	refs := map[string]string{}
	for i, e := range t.Output.Evidence {
		old := e.ID
		if known, ok := bank[old]; !ok || !sameEvidenceIdentity(known, e) {
			e.ID = t.TaskID + ":" + boundedRunes(old, 130)
			// Re-fetching the same source bytes is not new evidence just because
			// another delegation returned it under a fresh task-local identifier.
			for _, prior := range sortedEvidence(bank) {
				candidate := e
				candidate.ID = prior.ID
				if sameEvidenceIdentity(prior, candidate) {
					e.ID = prior.ID
					break
				}
			}
		}
		e.Summary = cleanDynamicText(e.Summary, maxEvidenceSummaryRunes)
		t.Output.Evidence[i] = e
		refs[old] = e.ID
	}
	t.Output.Summary = cleanDynamicText(t.Output.Summary, maxAgentSummaryRunes)
	for i := range t.Output.FollowUps {
		t.Output.FollowUps[i] = cleanDynamicText(t.Output.FollowUps[i], 500)
	}
	for i := range t.Output.Claims {
		c := &t.Output.Claims[i]
		c.ID = t.TaskID + ":" + boundedRunes(c.ID, 130)
		c.Statement = cleanDynamicText(c.Statement, maxClaimStatementRunes)
		for j, id := range c.EvidenceRefs {
			if replacement, ok := refs[id]; ok {
				c.EvidenceRefs[j] = replacement
			}
		}
	}
}

func (s *DynamicSupervisor) audit(ctx context.Context, input ExecutionInput, id, name string, data any, status, reason string) error {
	if s.auditor == nil {
		return nil
	}
	raw, _ := json.Marshal(data)
	hash := sha256.Sum256(raw)
	callID := input.TraceID + ":" + id
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	return s.auditor.Record(auditCtx, toolruntime.Invocation{CallID: callID, TraceID: input.TraceID, Intent: "collaboration", Strategy: DynamicVersion, Principal: toolruntime.Principal{TenantID: input.TenantID, UserID: input.UserID}}, toolruntime.ToolMessage{CallID: callID, ToolName: name, ToolVersion: DynamicVersion, ArgsHash: hex.EncodeToString(hash[:]), Status: status, ErrorCode: reason})
}

func dynamicAnswer(s SynthesisResult) string {
	if len(s.Claims) == 0 {
		return "当前没有足够的可引用结果；请补充下方所列信息。未执行服务器操作，也未自动回退其他策略。"
	}
	var b strings.Builder
	b.WriteString("以下为有来源的知识结论与待验证诊断假设；引用有效不等于根因已确认。")
	for i, c := range s.Claims {
		fmt.Fprintf(&b, "\n%d. %s [%s]", i+1, c.Statement, strings.Join(c.CitationIDs, ","))
	}
	return b.String()
}
