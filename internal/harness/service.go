package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultRunTimeout = 60 * time.Second
	MaxRunTimeout     = 10 * time.Minute
)

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type IDGenerator interface {
	NewID() string
}

type UUIDGenerator struct{}

func (UUIDGenerator) NewID() string { return uuid.NewString() }

type CreateCommand struct {
	TenantID        string
	UserID          string
	ClientRequestID string
	RequestID       string
	TraceID         string
	SessionID       string
	Intent          string
	Strategy        string
	PolicyVersion   string
	Timeout         time.Duration
	Budget          Budget
	Goal            string
}

type AdvanceCommand struct {
	RunID           string
	UserID          string
	ExpectedState   State
	ExpectedVersion int64
	NextState       State
	StepID          string
	StepKind        string
	ReasonCode      string
	PublicSummary   string
	EvidenceRefs    []string
	ToolCallIDs     []string
	BudgetDelta     BudgetDelta
	Checkpoint      CheckpointState
	ActionSignature string
	NewEvidence     bool
	TerminalReason  string
	ErrorCode       string
	CommandID       string
	CommandKind     string
}

type Service struct {
	repository Repository
	clock      Clock
	ids        IDGenerator
}

func NewService(repository Repository, clock Clock, ids IDGenerator) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("harness repository is required")
	}
	if clock == nil {
		clock = SystemClock{}
	}
	if ids == nil {
		ids = UUIDGenerator{}
	}
	return &Service{repository: repository, clock: clock, ids: ids}, nil
}

func (service *Service) Create(ctx context.Context, command CreateCommand) (RunDetail, bool, error) {
	command.TenantID = strings.TrimSpace(command.TenantID)
	command.UserID = strings.TrimSpace(command.UserID)
	command.ClientRequestID = strings.TrimSpace(command.ClientRequestID)
	command.RequestID = strings.TrimSpace(command.RequestID)
	command.TraceID = strings.TrimSpace(command.TraceID)
	command.Intent = strings.TrimSpace(command.Intent)
	command.Strategy = strings.TrimSpace(command.Strategy)
	command.PolicyVersion = strings.TrimSpace(command.PolicyVersion)
	command.Goal = strings.TrimSpace(command.Goal)
	if command.TenantID == "" || command.UserID == "" || command.ClientRequestID == "" || command.RequestID == "" || command.TraceID == "" {
		return RunDetail{}, false, fmt.Errorf("tenant, user, client request, request and trace ids are required")
	}
	if command.Intent == "" || command.Strategy == "" || command.PolicyVersion == "" || command.Goal == "" {
		return RunDetail{}, false, fmt.Errorf("intent, strategy, policy version and goal are required")
	}
	if len(command.ClientRequestID) > 128 || len(command.RequestID) > 128 || len([]rune(command.Goal)) > 500 {
		return RunDetail{}, false, fmt.Errorf("run identity or goal exceeds its bound")
	}
	if command.Timeout == 0 {
		command.Timeout = DefaultRunTimeout
	}
	if command.Timeout < time.Second || command.Timeout > MaxRunTimeout {
		return RunDetail{}, false, fmt.Errorf("run timeout must be between one second and ten minutes")
	}
	if command.Budget == (Budget{}) {
		command.Budget = DefaultBudget()
	}
	if err := command.Budget.Validate(); err != nil {
		return RunDetail{}, false, err
	}

	now := service.clock.Now().UTC()
	finished := now
	run := Run{
		RunID: service.ids.NewID(), TenantIDHash: PrincipalHash(command.TenantID), UserIDHash: PrincipalHash(command.UserID),
		ClientRequestID: command.ClientRequestID, RequestID: command.RequestID, TraceID: command.TraceID, SessionID: strings.TrimSpace(command.SessionID),
		Intent: command.Intent, Strategy: command.Strategy, PolicyVersion: command.PolicyVersion, HarnessVersion: Version, ContextVersion: ContextVersion,
		State: StateReceived, StateVersion: 1, CurrentStepID: "run-received", Budget: command.Budget,
		StartedAt: now, DeadlineAt: now.Add(command.Timeout), CreatedAt: now, UpdatedAt: now,
	}
	step := PublicStep{StepID: "run-received", Attempt: 1, Kind: "lifecycle", Status: "completed", ReasonCode: "REQUEST_ACCEPTED", PublicSummary: "诊断请求已接收并建立可恢复运行记录。", StateVersion: 1, StartedAt: now, FinishedAt: &finished}
	checkpoint := CheckpointState{Goal: command.Goal, Constraints: []string{"只执行只读验证", "证据不足时不宣称根因"}, NextAction: "assemble_context"}
	persisted, created, err := service.repository.CreateIdempotent(ctx, run, step, checkpoint)
	if err != nil {
		return RunDetail{}, false, err
	}
	detail, err := service.Get(ctx, persisted.RunID, command.UserID)
	return detail, created, err
}

func (service *Service) Get(ctx context.Context, runID string, userID string) (RunDetail, error) {
	run, err := service.repository.GetOwned(ctx, strings.TrimSpace(runID), PrincipalHash(userID))
	if err != nil {
		return RunDetail{}, err
	}
	steps, err := service.repository.ListSteps(ctx, run.RunID)
	if err != nil {
		return RunDetail{}, err
	}
	checkpoint, err := service.repository.LatestCheckpoint(ctx, run.RunID)
	if err != nil {
		return RunDetail{}, err
	}
	return RunDetail{Run: run, Steps: steps, Checkpoint: checkpoint}, nil
}

func (service *Service) Advance(ctx context.Context, command AdvanceCommand) (Run, error) {
	run, err := service.repository.GetOwned(ctx, strings.TrimSpace(command.RunID), PrincipalHash(command.UserID))
	if err != nil {
		return Run{}, err
	}
	if run.State != command.ExpectedState || run.StateVersion != command.ExpectedVersion {
		if command.CommandID != "" && run.LastCommandID == command.CommandID && run.LastCommandKind == command.CommandKind {
			return run, nil
		}
		return Run{}, ErrRunConflict
	}
	if service.clock.Now().UTC().After(run.DeadlineAt) && run.State == StateRunning {
		return service.transitionBudgetTerminal(ctx, run, command, "TIME_BUDGET_EXCEEDED")
	}
	nextBudget := run.Budget
	nextBudget.UsedIterations += command.BudgetDelta.Iterations
	nextBudget.UsedToolCalls += command.BudgetDelta.ToolCalls
	nextBudget.UsedInputTokens += command.BudgetDelta.InputTokens
	nextBudget.UsedOutputTokens += command.BudgetDelta.OutputTokens
	nextBudget.UsedCostMicros += command.BudgetDelta.CostMicros
	if err := nextBudget.Validate(); errors.Is(err, ErrBudgetExceeded) && run.State == StateRunning {
		return service.transitionBudgetTerminal(ctx, run, command, "EXECUTION_BUDGET_EXCEEDED")
	} else if err != nil {
		return Run{}, err
	}
	checkpoint := command.Checkpoint
	if checkpoint.Goal == "" {
		previous, checkpointErr := service.repository.LatestCheckpoint(ctx, run.RunID)
		if checkpointErr != nil {
			return Run{}, checkpointErr
		}
		if previous != nil {
			checkpoint = *previous
		}
	}
	if command.ActionSignature != "" {
		if checkpoint.LastActionSignature == command.ActionSignature && !command.NewEvidence {
			checkpoint.RepeatedActionCount++
		} else {
			checkpoint.LastActionSignature = command.ActionSignature
			checkpoint.RepeatedActionCount = 0
		}
		if checkpoint.RepeatedActionCount >= 2 && run.State == StateRunning {
			command.Checkpoint = checkpoint
			return service.transitionBudgetTerminal(ctx, run, command, "NO_PROGRESS")
		}
	}
	now := service.clock.Now().UTC()
	finished := now
	step := PublicStep{StepID: command.StepID, Attempt: 1, Kind: command.StepKind, Status: "completed", ReasonCode: command.ReasonCode, PublicSummary: command.PublicSummary, EvidenceRefs: command.EvidenceRefs, ToolCallIDs: command.ToolCallIDs, BudgetDelta: command.BudgetDelta, StartedAt: now, FinishedAt: &finished}
	return service.repository.TransitionCAS(ctx, Transition{RunID: run.RunID, UserIDHash: run.UserIDHash, ExpectedState: run.State, ExpectedVersion: run.StateVersion, NextState: command.NextState, Step: step, Checkpoint: checkpoint, BudgetDelta: command.BudgetDelta, TerminalReason: command.TerminalReason, ErrorCode: command.ErrorCode, CommandID: command.CommandID, CommandKind: command.CommandKind, At: now})
}

func (service *Service) Cancel(ctx context.Context, runID string, userID string) (RunDetail, error) {
	run, err := service.repository.GetOwned(ctx, strings.TrimSpace(runID), PrincipalHash(userID))
	if err != nil {
		return RunDetail{}, err
	}
	if IsTerminal(run.State) {
		return service.Get(ctx, run.RunID, userID)
	}
	checkpoint, err := service.repository.LatestCheckpoint(ctx, run.RunID)
	if err != nil {
		return RunDetail{}, err
	}
	if checkpoint == nil {
		checkpoint = &CheckpointState{Goal: "cancel run"}
	}
	_, err = service.Advance(ctx, AdvanceCommand{RunID: run.RunID, UserID: userID, ExpectedState: run.State, ExpectedVersion: run.StateVersion, NextState: StateCancelled, StepID: fmt.Sprintf("cancel-%d", run.StateVersion+1), StepKind: "lifecycle", ReasonCode: "USER_CANCELLED", PublicSummary: "运行已取消，后续步骤不会继续执行。", Checkpoint: *checkpoint, TerminalReason: "USER_CANCELLED"})
	if errors.Is(err, ErrRunConflict) {
		latest, getErr := service.Get(ctx, run.RunID, userID)
		if getErr == nil && latest.Run.State == StateCancelled {
			return latest, nil
		}
	}
	if err != nil {
		return RunDetail{}, err
	}
	return service.Get(ctx, run.RunID, userID)
}

func (service *Service) transitionBudgetTerminal(ctx context.Context, run Run, command AdvanceCommand, reason string) (Run, error) {
	checkpoint := command.Checkpoint
	if checkpoint.Goal == "" {
		previous, err := service.repository.LatestCheckpoint(ctx, run.RunID)
		if err != nil {
			return Run{}, err
		}
		if previous != nil {
			checkpoint = *previous
		}
	}
	now := service.clock.Now().UTC()
	finished := now
	stepID := command.StepID
	if stepID == "" {
		stepID = fmt.Sprintf("budget-stop-%d", run.StateVersion+1)
	}
	step := PublicStep{StepID: stepID, Attempt: 1, Kind: "guardrail", Status: "stopped", ReasonCode: reason, PublicSummary: "运行已由预算或无进展护栏安全终止。", ErrorCode: reason, StartedAt: now, FinishedAt: &finished}
	return service.repository.TransitionCAS(ctx, Transition{RunID: run.RunID, UserIDHash: run.UserIDHash, ExpectedState: run.State, ExpectedVersion: run.StateVersion, NextState: StateBudgetExceeded, Step: step, Checkpoint: checkpoint, TerminalReason: reason, ErrorCode: reason, At: now})
}

func PrincipalHash(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}
