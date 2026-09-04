package diagnostic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"GopherAI/internal/harness"
)

const (
	StrategyName  = "diagnosis_standard"
	PolicyVersion = "policy-diagnostic-v1"
	IntentName    = "troubleshooting"
	artifactType  = "diagnostic-state-v1"
)

type StartCommand struct {
	TenantID        string
	UserID          string
	ClientRequestID string
	RequestID       string
	TraceID         string
	SessionID       string
	Message         string
}

type ResumeCommand struct {
	RunID           string
	UserID          string
	ClientRequestID string
	ExpectedVersion int64
	Message         string
}

type RunResponse struct {
	Created bool              `json:"created"`
	Detail  harness.RunDetail `json:"detail"`
	Result  *Result           `json:"result,omitempty"`
}

type persistedState struct {
	Extracted ExtractedInput `json:"extracted"`
	Result    *Result        `json:"result,omitempty"`
}

type Workflow struct {
	lifecycle *harness.Service
	agent     *Agent
	running   sync.Map
}

func NewWorkflow(lifecycle *harness.Service, agent *Agent) (*Workflow, error) {
	if lifecycle == nil {
		return nil, fmt.Errorf("lifecycle service is required")
	}
	if agent == nil {
		agent = NewAgent()
	}
	return &Workflow{lifecycle: lifecycle, agent: agent}, nil
}

func (workflow *Workflow) Start(ctx context.Context, command StartCommand) (RunResponse, error) {
	message := strings.TrimSpace(command.Message)
	if message == "" {
		return RunResponse{}, ErrEmptyDiagnosticInput
	}
	detail, created, err := workflow.lifecycle.Create(ctx, harness.CreateCommand{
		TenantID: command.TenantID, UserID: command.UserID, ClientRequestID: command.ClientRequestID,
		RequestID: command.RequestID, TraceID: command.TraceID, SessionID: command.SessionID,
		Intent: IntentName, Strategy: StrategyName, PolicyVersion: PolicyVersion,
		Goal: "根据用户提供的可观察证据形成可验证的故障假设",
	})
	if err != nil {
		return RunResponse{}, err
	}
	if harness.IsTerminal(detail.Run.State) || detail.Run.State == harness.StateWaitingUser {
		return responseFromDetail(detail, created)
	}
	return workflow.execute(ctx, detail, command.UserID, message, created)
}

func (workflow *Workflow) Resume(ctx context.Context, command ResumeCommand) (RunResponse, error) {
	message := strings.TrimSpace(command.Message)
	if message == "" || strings.TrimSpace(command.ClientRequestID) == "" {
		return RunResponse{}, fmt.Errorf("resume message and client request id are required")
	}
	detail, err := workflow.lifecycle.Get(ctx, command.RunID, command.UserID)
	if err != nil {
		return RunResponse{}, err
	}
	if detail.Run.LastCommandID == command.ClientRequestID && detail.Run.LastCommandKind == "resume" {
		return responseFromDetail(detail, false)
	}
	if detail.Run.State != harness.StateWaitingUser || detail.Run.StateVersion != command.ExpectedVersion {
		return RunResponse{}, harness.ErrRunConflict
	}
	state, err := stateFromCheckpoint(detail.Checkpoint)
	if err != nil {
		return RunResponse{}, err
	}
	combined := strings.TrimSpace(state.Extracted.SanitizedExcerpt + "\n" + message)
	extracted, result, err := workflow.agent.Analyze(combined)
	if err != nil {
		return RunResponse{}, err
	}
	checkpoint, err := diagnosticCheckpoint(detail.Checkpoint, extracted, &result, "plan_diagnosis")
	if err != nil {
		return RunResponse{}, err
	}
	_, err = workflow.lifecycle.Advance(ctx, harness.AdvanceCommand{
		RunID: detail.Run.RunID, UserID: command.UserID, ExpectedState: harness.StateWaitingUser, ExpectedVersion: command.ExpectedVersion,
		NextState: harness.StateContextReady, StepID: fmt.Sprintf("resume-context-%d", command.ExpectedVersion+1), StepKind: "context",
		ReasonCode: "USER_CONTEXT_ADDED", PublicSummary: "已合并用户补充信息并重新生成脱敏诊断上下文。",
		BudgetDelta: harness.BudgetDelta{Iterations: 1, InputTokens: approximateTokens(message)}, Checkpoint: checkpoint,
		ActionSignature: "resume:" + strings.TrimSpace(command.ClientRequestID), NewEvidence: true,
		CommandID: command.ClientRequestID, CommandKind: "resume",
	})
	if err != nil {
		return RunResponse{}, err
	}
	detail, err = workflow.lifecycle.Get(ctx, detail.Run.RunID, command.UserID)
	if err != nil {
		return RunResponse{}, err
	}
	return workflow.execute(ctx, detail, command.UserID, combined, false)
}

func (workflow *Workflow) Get(ctx context.Context, runID string, userID string) (RunResponse, error) {
	detail, err := workflow.lifecycle.Get(ctx, runID, userID)
	if err != nil {
		return RunResponse{}, err
	}
	return responseFromDetail(detail, false)
}

func (workflow *Workflow) Cancel(ctx context.Context, runID string, userID string) (RunResponse, error) {
	if value, exists := workflow.running.Load(runID); exists {
		value.(context.CancelFunc)()
	}
	detail, err := workflow.lifecycle.Cancel(ctx, runID, userID)
	if err != nil {
		return RunResponse{}, err
	}
	return responseFromDetail(detail, false)
}

func (workflow *Workflow) execute(parent context.Context, detail harness.RunDetail, userID string, raw string, created bool) (RunResponse, error) {
	ctx, cancel := context.WithCancel(parent)
	workflow.running.Store(detail.Run.RunID, cancel)
	defer func() {
		cancel()
		workflow.running.Delete(detail.Run.RunID)
	}()

	state, _ := stateFromCheckpoint(detail.Checkpoint)
	var extracted ExtractedInput
	var result Result
	var err error
	if state.Extracted.Version == ExtractorVersion {
		extracted = state.Extracted
		if state.Result != nil {
			result = *state.Result
		}
	} else {
		extracted, result, err = workflow.agent.Analyze(raw)
		if err != nil {
			return RunResponse{}, err
		}
	}

	if detail.Run.State == harness.StateReceived {
		checkpoint, checkpointErr := diagnosticCheckpoint(detail.Checkpoint, extracted, &result, "plan_diagnosis")
		if checkpointErr != nil {
			return RunResponse{}, checkpointErr
		}
		_, err = workflow.advanceOrCancel(ctx, detail.Run, userID, harness.AdvanceCommand{
			NextState: harness.StateContextReady, StepID: "context-ready", StepKind: "context", ReasonCode: "INPUT_SANITIZED",
			PublicSummary: fmt.Sprintf("已完成输入脱敏与结构化：识别 %d 个组件、%d 个错误特征，移除 %d 处敏感信息。", len(extracted.Components), len(extracted.ErrorSignatures), extracted.RedactionCount),
			BudgetDelta:   harness.BudgetDelta{Iterations: 1, InputTokens: approximateTokens(extracted.SanitizedExcerpt)}, Checkpoint: checkpoint,
			ActionSignature: "extract:" + ExtractorVersion, NewEvidence: true,
		})
		if err != nil {
			return workflow.afterAdvanceError(detail.Run.RunID, userID, err)
		}
		detail, err = workflow.lifecycle.Get(ctx, detail.Run.RunID, userID)
		if err != nil {
			return RunResponse{}, err
		}
	}

	if detail.Run.State == harness.StateContextReady {
		checkpoint, checkpointErr := diagnosticCheckpoint(detail.Checkpoint, extracted, &result, "execute_diagnosis")
		if checkpointErr != nil {
			return RunResponse{}, checkpointErr
		}
		_, err = workflow.advanceOrCancel(ctx, detail.Run, userID, harness.AdvanceCommand{
			NextState: harness.StatePlanned, StepID: fmt.Sprintf("plan-%d", detail.Run.StateVersion+1), StepKind: "plan", ReasonCode: "BOUNDED_PLAN_READY",
			PublicSummary: "已生成有界诊断计划：最多三个假设，每个假设必须带证据与只读验证步骤。",
			Checkpoint:    checkpoint, ActionSignature: "plan:" + AgentVersion, NewEvidence: false,
		})
		if err != nil {
			return workflow.afterAdvanceError(detail.Run.RunID, userID, err)
		}
		detail, err = workflow.lifecycle.Get(ctx, detail.Run.RunID, userID)
		if err != nil {
			return RunResponse{}, err
		}
	}

	if detail.Run.State == harness.StatePlanned {
		checkpoint, checkpointErr := diagnosticCheckpoint(detail.Checkpoint, extracted, &result, "publish_result")
		if checkpointErr != nil {
			return RunResponse{}, checkpointErr
		}
		_, err = workflow.advanceOrCancel(ctx, detail.Run, userID, harness.AdvanceCommand{
			NextState: harness.StateRunning, StepID: fmt.Sprintf("diagnose-%d", detail.Run.StateVersion+1), StepKind: "diagnose", ReasonCode: "DIAGNOSTIC_AGENT_STARTED",
			PublicSummary: "DiagnosticAgent 正在依据错误特征匹配故障知识并执行证据门检查。",
			Checkpoint:    checkpoint, ActionSignature: "diagnose:" + AgentVersion, NewEvidence: len(result.Hypotheses) > 0,
		})
		if err != nil {
			return workflow.afterAdvanceError(detail.Run.RunID, userID, err)
		}
		detail, err = workflow.lifecycle.Get(ctx, detail.Run.RunID, userID)
		if err != nil {
			return RunResponse{}, err
		}
	}

	if detail.Run.State == harness.StateRunning {
		checkpoint, checkpointErr := diagnosticCheckpoint(detail.Checkpoint, extracted, &result, "await_user_evidence")
		if checkpointErr != nil {
			return RunResponse{}, checkpointErr
		}
		nextState := harness.StateSucceeded
		reason := "EVIDENCE_BACKED_HYPOTHESES_READY"
		summary := fmt.Sprintf("已形成 %d 个待验证假设；结论保持为 hypothesis，未把观察信息升级为已确认根因。", len(result.Hypotheses))
		terminalReason := "DIAGNOSTIC_HYPOTHESES_READY"
		if result.NeedsUserInput {
			nextState = harness.StateWaitingUser
			reason = "CRITICAL_EVIDENCE_MISSING"
			summary = fmt.Sprintf("诊断仍缺少关键验证信息，已保留现有假设、暂停运行并提出 %d 个补充问题。", len(result.MissingInformation))
			terminalReason = ""
		}
		_, err = workflow.advanceOrCancel(ctx, detail.Run, userID, harness.AdvanceCommand{
			NextState: nextState, StepID: fmt.Sprintf("result-%d", detail.Run.StateVersion+1), StepKind: "answer", ReasonCode: reason,
			PublicSummary: summary, EvidenceRefs: resultEvidenceRefs(result), BudgetDelta: harness.BudgetDelta{Iterations: 1},
			Checkpoint: checkpoint, ActionSignature: "publish:" + result.ConclusionStatus, NewEvidence: len(result.Hypotheses) > 0,
			TerminalReason: terminalReason,
		})
		if err != nil {
			return workflow.afterAdvanceError(detail.Run.RunID, userID, err)
		}
	}
	detail, err = workflow.lifecycle.Get(context.Background(), detail.Run.RunID, userID)
	if err != nil {
		return RunResponse{}, err
	}
	return responseFromDetail(detail, created)
}

func (workflow *Workflow) advanceOrCancel(ctx context.Context, run harness.Run, userID string, command harness.AdvanceCommand) (harness.Run, error) {
	if err := ctx.Err(); err != nil {
		_, cancelErr := workflow.lifecycle.Cancel(context.Background(), run.RunID, userID)
		if cancelErr != nil && !errors.Is(cancelErr, harness.ErrRunConflict) {
			return harness.Run{}, cancelErr
		}
		return harness.Run{}, context.Canceled
	}
	command.RunID = run.RunID
	command.UserID = userID
	command.ExpectedState = run.State
	command.ExpectedVersion = run.StateVersion
	return workflow.lifecycle.Advance(ctx, command)
}

func (workflow *Workflow) afterAdvanceError(runID string, userID string, err error) (RunResponse, error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, harness.ErrRunConflict) {
		detail, getErr := workflow.lifecycle.Get(context.Background(), runID, userID)
		if getErr == nil && detail.Run.State == harness.StateCancelled {
			response, responseErr := responseFromDetail(detail, false)
			return response, responseErr
		}
	}
	return RunResponse{}, err
}

func diagnosticCheckpoint(previous *harness.CheckpointState, extracted ExtractedInput, result *Result, nextAction string) (harness.CheckpointState, error) {
	state := persistedState{Extracted: extracted, Result: result}
	artifact, err := json.Marshal(state)
	if err != nil {
		return harness.CheckpointState{}, err
	}
	checkpoint := harness.CheckpointState{
		Goal: "根据用户提供的可观察证据形成可验证的故障假设", Constraints: []string{"最多三个假设", "仅提供只读验证", "证据不足时暂停追问"},
		ConfirmedFacts: map[string]string{
			"components": strings.Join(extracted.Components, ","), "error_signatures": strings.Join(extracted.ErrorSignatures, ","),
			"redaction_count": fmt.Sprintf("%d", extracted.RedactionCount),
		},
		NextAction: nextAction, ArtifactType: artifactType, Artifact: artifact,
	}
	if result != nil {
		checkpoint.OpenQuestions = make([]string, 0, len(result.MissingInformation))
		for _, missing := range result.MissingInformation {
			checkpoint.OpenQuestions = append(checkpoint.OpenQuestions, missing.Question)
		}
		checkpoint.EvidenceRefs = resultEvidenceRefs(*result)
	}
	if previous != nil {
		checkpoint.CompletedSteps = append([]string(nil), previous.CompletedSteps...)
		checkpoint.FailedSteps = append([]string(nil), previous.FailedSteps...)
		checkpoint.LastActionSignature = previous.LastActionSignature
		checkpoint.RepeatedActionCount = previous.RepeatedActionCount
	}
	return checkpoint, nil
}

func stateFromCheckpoint(checkpoint *harness.CheckpointState) (persistedState, error) {
	if checkpoint == nil || checkpoint.ArtifactType == "" || len(checkpoint.Artifact) == 0 {
		return persistedState{}, nil
	}
	if checkpoint.ArtifactType != artifactType {
		return persistedState{}, fmt.Errorf("unsupported diagnostic checkpoint artifact %q", checkpoint.ArtifactType)
	}
	var state persistedState
	if err := json.Unmarshal(checkpoint.Artifact, &state); err != nil {
		return persistedState{}, err
	}
	return state, nil
}

func responseFromDetail(detail harness.RunDetail, created bool) (RunResponse, error) {
	state, err := stateFromCheckpoint(detail.Checkpoint)
	if err != nil {
		return RunResponse{}, err
	}
	return RunResponse{Created: created, Detail: detail, Result: state.Result}, nil
}

func resultEvidenceRefs(result Result) []string {
	seen := map[string]struct{}{}
	refs := make([]string, 0, len(result.Hypotheses))
	for _, hypothesis := range result.Hypotheses {
		for _, evidence := range hypothesis.Evidence {
			if _, exists := seen[evidence.ID]; !exists {
				seen[evidence.ID] = struct{}{}
				refs = append(refs, evidence.ID)
			}
		}
	}
	return refs
}

func approximateTokens(value string) int {
	runes := len([]rune(value))
	if runes == 0 {
		return 0
	}
	return (runes + 2) / 3
}
