package harness

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fixedClock struct{ now time.Time }

func (clock *fixedClock) Now() time.Time { return clock.now }

type sequenceIDs struct {
	mu   sync.Mutex
	next int
}

type recordingObserver struct {
	creates     []bool
	transitions [][2]State
}

func (observer *recordingObserver) RecordRunCreate(_ Run, created bool) {
	observer.creates = append(observer.creates, created)
}

func (observer *recordingObserver) RecordRunTransition(previous Run, current Run) {
	observer.transitions = append(observer.transitions, [2]State{previous.State, current.State})
}

func (ids *sequenceIDs) NewID() string {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.next++
	return fmt.Sprintf("run-%d", ids.next)
}

type memoryRepository struct {
	mu          sync.Mutex
	runs        map[string]Run
	requestKeys map[string]string
	steps       map[string][]PublicStep
	checkpoints map[string][]CheckpointState
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{runs: map[string]Run{}, requestKeys: map[string]string{}, steps: map[string][]PublicStep{}, checkpoints: map[string][]CheckpointState{}}
}

func (repository *memoryRepository) CreateIdempotent(_ context.Context, run Run, step PublicStep, checkpoint CheckpointState) (Run, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	key := run.TenantIDHash + ":" + run.UserIDHash + ":" + run.ClientRequestID
	if runID, exists := repository.requestKeys[key]; exists {
		return repository.runs[runID], false, nil
	}
	repository.requestKeys[key] = run.RunID
	repository.runs[run.RunID] = run
	repository.steps[run.RunID] = []PublicStep{step}
	repository.checkpoints[run.RunID] = []CheckpointState{checkpoint}
	return run, true, nil
}

func (repository *memoryRepository) GetOwned(_ context.Context, runID string, userIDHash string) (Run, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	run, exists := repository.runs[runID]
	if !exists || run.UserIDHash != userIDHash {
		return Run{}, ErrRunNotFound
	}
	return run, nil
}

func (repository *memoryRepository) ListSteps(_ context.Context, runID string) ([]PublicStep, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]PublicStep(nil), repository.steps[runID]...), nil
}

func (repository *memoryRepository) LatestCheckpoint(_ context.Context, runID string) (*CheckpointState, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	items := repository.checkpoints[runID]
	if len(items) == 0 {
		return nil, nil
	}
	item := items[len(items)-1]
	return &item, nil
}

func (repository *memoryRepository) TransitionCAS(_ context.Context, transition Transition) (Run, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	run, exists := repository.runs[transition.RunID]
	if !exists || run.UserIDHash != transition.UserIDHash {
		return Run{}, ErrRunNotFound
	}
	if run.State != transition.ExpectedState || run.StateVersion != transition.ExpectedVersion {
		if transition.CommandID != "" && run.LastCommandID == transition.CommandID && run.LastCommandKind == transition.CommandKind {
			return run, nil
		}
		return Run{}, ErrRunConflict
	}
	if err := ValidateTransition(run.State, transition.NextState); err != nil {
		return Run{}, err
	}
	run.State = transition.NextState
	run.StateVersion++
	run.CurrentStepID = transition.Step.StepID
	run.NeedsUserInput = transition.NextState == StateWaitingUser
	run.TerminalReason = transition.TerminalReason
	run.LastErrorCode = transition.ErrorCode
	if transition.CommandID != "" {
		run.LastCommandID = transition.CommandID
		run.LastCommandKind = transition.CommandKind
	}
	run.Budget.UsedIterations += transition.BudgetDelta.Iterations
	run.Budget.UsedToolCalls += transition.BudgetDelta.ToolCalls
	run.Budget.UsedInputTokens += transition.BudgetDelta.InputTokens
	run.Budget.UsedOutputTokens += transition.BudgetDelta.OutputTokens
	run.Budget.UsedCostMicros += transition.BudgetDelta.CostMicros
	run.UpdatedAt = transition.At
	transition.Step.StateVersion = run.StateVersion
	if IsTerminal(run.State) {
		run.FinishedAt = &transition.At
	}
	repository.runs[run.RunID] = run
	repository.steps[run.RunID] = append(repository.steps[run.RunID], transition.Step)
	repository.checkpoints[run.RunID] = append(repository.checkpoints[run.RunID], transition.Checkpoint)
	return run, nil
}

func createTestService(t *testing.T) (*Service, *fixedClock) {
	t.Helper()
	clock := &fixedClock{now: time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)}
	service, err := NewService(newMemoryRepository(), clock, &sequenceIDs{})
	if err != nil {
		t.Fatal(err)
	}
	return service, clock
}

func createRun(t *testing.T, service *Service, clientID string) RunDetail {
	t.Helper()
	detail, _, err := service.Create(context.Background(), CreateCommand{TenantID: "alice", UserID: "alice", ClientRequestID: clientID, RequestID: clientID, TraceID: "trace-1", Intent: "troubleshooting", Strategy: "diagnosis_standard", PolicyVersion: "policy-diagnostic-v1", Goal: "diagnose the supplied symptom"})
	if err != nil {
		t.Fatal(err)
	}
	return detail
}

func TestCreateIsIdempotentPerPrincipalAndClientRequest(t *testing.T) {
	service, _ := createTestService(t)
	first, created, err := service.Create(context.Background(), CreateCommand{TenantID: "alice", UserID: "alice", ClientRequestID: "request-1", RequestID: "request-1", TraceID: "trace-1", Intent: "troubleshooting", Strategy: "diagnosis_standard", PolicyVersion: "policy-diagnostic-v1", Goal: "diagnose"})
	if err != nil || !created {
		t.Fatalf("first create failed: created=%v err=%v", created, err)
	}
	second, created, err := service.Create(context.Background(), CreateCommand{TenantID: "alice", UserID: "alice", ClientRequestID: "request-1", RequestID: "request-2", TraceID: "trace-2", Intent: "troubleshooting", Strategy: "diagnosis_standard", PolicyVersion: "policy-diagnostic-v1", Goal: "diagnose again"})
	if err != nil || created || second.Run.RunID != first.Run.RunID || len(second.Steps) != 1 {
		t.Fatalf("idempotent replay duplicated a run: first=%#v second=%#v created=%v err=%v", first.Run, second.Run, created, err)
	}
}

func TestStateVersionIsMonotonicAndStaleTransitionConflicts(t *testing.T) {
	service, _ := createTestService(t)
	run := createRun(t, service, "request-2").Run
	next, err := service.Advance(context.Background(), AdvanceCommand{RunID: run.RunID, UserID: "alice", ExpectedState: StateReceived, ExpectedVersion: 1, NextState: StateContextReady, StepID: "context-ready", StepKind: "context", ReasonCode: "INPUT_SANITIZED", PublicSummary: "上下文已完成脱敏和结构化。", Checkpoint: CheckpointState{Goal: "diagnose", NextAction: "plan"}, BudgetDelta: BudgetDelta{Iterations: 1}})
	if err != nil || next.StateVersion != 2 {
		t.Fatalf("advance failed: %#v %v", next, err)
	}
	_, err = service.Advance(context.Background(), AdvanceCommand{RunID: run.RunID, UserID: "alice", ExpectedState: StateReceived, ExpectedVersion: 1, NextState: StateContextReady, StepID: "stale", StepKind: "context", Checkpoint: CheckpointState{Goal: "diagnose"}})
	if !errors.Is(err, ErrRunConflict) {
		t.Fatalf("expected stale version conflict, got %v", err)
	}
}

func TestObserverSeesOnlyDurableCreatesAndTransitions(t *testing.T) {
	repository := newMemoryRepository()
	observer := new(recordingObserver)
	service, err := NewObservedService(repository, &fixedClock{now: time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)}, &sequenceIDs{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	detail := createRun(t, service, "observed-request")
	if _, _, err := service.Create(context.Background(), CreateCommand{TenantID: "alice", UserID: "alice", ClientRequestID: "observed-request", RequestID: "retry", TraceID: "retry", Intent: "troubleshooting", Strategy: "diagnosis_standard", PolicyVersion: "policy-diagnostic-v1", Goal: "retry"}); err != nil {
		t.Fatal(err)
	}
	if len(observer.creates) != 2 || !observer.creates[0] || observer.creates[1] {
		t.Fatalf("unexpected create observations: %#v", observer.creates)
	}
	command := AdvanceCommand{RunID: detail.Run.RunID, UserID: "alice", ExpectedState: StateReceived, ExpectedVersion: 1, NextState: StateContextReady, StepID: "context", StepKind: "context", PublicSummary: "context", Checkpoint: CheckpointState{Goal: "diagnose"}}
	if _, err := service.Advance(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Advance(context.Background(), command); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("expected stale conflict, got %v", err)
	}
	if len(observer.transitions) != 1 || observer.transitions[0] != [2]State{StateReceived, StateContextReady} {
		t.Fatalf("observer recorded a failed or duplicate transition: %#v", observer.transitions)
	}
}

func TestCancelIsIdempotentAndOwned(t *testing.T) {
	service, _ := createTestService(t)
	run := createRun(t, service, "request-3").Run
	if _, err := service.Cancel(context.Background(), run.RunID, "mallory"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("cross-user cancel should be hidden as not found, got %v", err)
	}
	first, err := service.Cancel(context.Background(), run.RunID, "alice")
	if err != nil || first.Run.State != StateCancelled {
		t.Fatalf("cancel failed: %#v %v", first.Run, err)
	}
	second, err := service.Cancel(context.Background(), run.RunID, "alice")
	if err != nil || second.Run.StateVersion != first.Run.StateVersion || len(second.Steps) != len(first.Steps) {
		t.Fatalf("cancel replay changed state: first=%#v second=%#v err=%v", first, second, err)
	}
}

func TestCommandReplayReturnsAppliedStateWithoutDuplicateStep(t *testing.T) {
	service, _ := createTestService(t)
	run := createRun(t, service, "request-command-replay").Run
	command := AdvanceCommand{
		RunID: run.RunID, UserID: "alice", ExpectedState: StateReceived, ExpectedVersion: run.StateVersion,
		NextState: StateContextReady, StepID: "resume-context", StepKind: "context", PublicSummary: "context",
		Checkpoint: CheckpointState{Goal: "diagnose"}, CommandID: "resume-request-1", CommandKind: "resume",
	}
	first, err := service.Advance(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Advance(context.Background(), command)
	if err != nil || second.StateVersion != first.StateVersion {
		t.Fatalf("command replay changed state: first=%#v second=%#v err=%v", first, second, err)
	}
	detail, err := service.Get(context.Background(), run.RunID, "alice")
	if err != nil || len(detail.Steps) != 2 {
		t.Fatalf("command replay duplicated public step: steps=%d err=%v", len(detail.Steps), err)
	}
}

func TestRunningRunStopsAtHardBudget(t *testing.T) {
	service, _ := createTestService(t)
	run := createRun(t, service, "request-4").Run
	for _, item := range []struct {
		from State
		to   State
		id   string
	}{
		{StateReceived, StateContextReady, "context"},
		{StateContextReady, StatePlanned, "plan"},
		{StatePlanned, StateRunning, "run"},
	} {
		var err error
		run, err = service.Advance(context.Background(), AdvanceCommand{RunID: run.RunID, UserID: "alice", ExpectedState: item.from, ExpectedVersion: run.StateVersion, NextState: item.to, StepID: item.id, StepKind: "lifecycle", PublicSummary: item.id, Checkpoint: CheckpointState{Goal: "diagnose"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	run.Budget.UsedIterations = run.Budget.MaxIterations
	service.repository.(*memoryRepository).runs[run.RunID] = run
	stopped, err := service.Advance(context.Background(), AdvanceCommand{RunID: run.RunID, UserID: "alice", ExpectedState: StateRunning, ExpectedVersion: run.StateVersion, NextState: StateSucceeded, StepID: "answer", StepKind: "answer", PublicSummary: "answer", Checkpoint: CheckpointState{Goal: "diagnose"}, BudgetDelta: BudgetDelta{Iterations: 1}})
	if err != nil || stopped.State != StateBudgetExceeded || stopped.TerminalReason != "EXECUTION_BUDGET_EXCEEDED" {
		t.Fatalf("hard budget did not stop run: %#v %v", stopped, err)
	}
}
