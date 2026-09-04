package diagnostic

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"GopherAI/internal/harness"
)

type cancellationLifecycle struct {
	mu              sync.Mutex
	detail          harness.RunDetail
	advanceCalls    int
	explicitCancels int
	contextCancels  int
}

func newCancellationLifecycle() *cancellationLifecycle {
	now := time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)
	return &cancellationLifecycle{detail: harness.RunDetail{
		Run: harness.Run{
			RunID: "run-cancel-1", State: harness.StateReceived, StateVersion: 1,
			Intent: IntentName, Strategy: StrategyName, PolicyVersion: PolicyVersion,
			Budget: harness.DefaultBudget(), StartedAt: now, UpdatedAt: now, DeadlineAt: now.Add(time.Minute),
		},
		Checkpoint: &harness.CheckpointState{Goal: "diagnose"},
	}}
}

func (lifecycle *cancellationLifecycle) Create(context.Context, harness.CreateCommand) (harness.RunDetail, bool, error) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	return lifecycle.detail, true, nil
}

func (lifecycle *cancellationLifecycle) Get(context.Context, string, string) (harness.RunDetail, error) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	return lifecycle.detail, nil
}

func (lifecycle *cancellationLifecycle) Advance(_ context.Context, command harness.AdvanceCommand) (harness.Run, error) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	lifecycle.advanceCalls++
	lifecycle.detail.Run.State = command.NextState
	lifecycle.detail.Run.StateVersion++
	return lifecycle.detail.Run, nil
}

func (lifecycle *cancellationLifecycle) Cancel(_ context.Context, _ string, _ string) (harness.RunDetail, error) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	lifecycle.explicitCancels++
	lifecycle.cancelLocked("USER_CANCELLED")
	return lifecycle.detail, nil
}

func (lifecycle *cancellationLifecycle) CancelDueToContext(_ context.Context, _ string, _ string) (harness.RunDetail, error) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	lifecycle.contextCancels++
	lifecycle.cancelLocked("REQUEST_CONTEXT_CANCELLED")
	return lifecycle.detail, nil
}

func (lifecycle *cancellationLifecycle) cancelLocked(reason string) {
	if harness.IsTerminal(lifecycle.detail.Run.State) {
		return
	}
	lifecycle.detail.Run.State = harness.StateCancelled
	lifecycle.detail.Run.StateVersion++
	lifecycle.detail.Run.TerminalReason = reason
}

type blockingAnalyzer struct {
	started chan struct{}
	once    sync.Once
	active  atomic.Int32
}

func newBlockingAnalyzer() *blockingAnalyzer {
	return &blockingAnalyzer{started: make(chan struct{})}
}

func (analyzer *blockingAnalyzer) AnalyzeContext(ctx context.Context, _ string) (ExtractedInput, Result, error) {
	analyzer.active.Add(1)
	defer analyzer.active.Add(-1)
	analyzer.once.Do(func() { close(analyzer.started) })
	<-ctx.Done()
	return ExtractedInput{}, Result{}, ctx.Err()
}

func TestRequestContextCancellationConvergesRunAndStopsAnalyzer(t *testing.T) {
	lifecycle := newCancellationLifecycle()
	analyzer := newBlockingAnalyzer()
	workflow, err := NewWorkflow(lifecycle, analyzer)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan RunResponse, 1)
	errors := make(chan error, 1)
	go func() {
		response, startErr := workflow.Start(ctx, StartCommand{TenantID: "alice", UserID: "alice", ClientRequestID: "request-1", RequestID: "request-1", TraceID: "trace-1", Message: "symptom"})
		result <- response
		errors <- startErr
	}()
	awaitStarted(t, analyzer.started)
	cancel()
	response := awaitResponse(t, result)
	if startErr := <-errors; startErr != nil {
		t.Fatal(startErr)
	}
	if response.Detail.Run.State != harness.StateCancelled || response.Detail.Run.TerminalReason != "REQUEST_CONTEXT_CANCELLED" {
		t.Fatalf("request cancellation did not converge the run: %+v", response.Detail.Run)
	}
	if analyzer.active.Load() != 0 || lifecycle.advanceCalls != 0 || lifecycle.contextCancels != 1 {
		t.Fatalf("work leaked after disconnect: active=%d advances=%d context_cancels=%d", analyzer.active.Load(), lifecycle.advanceCalls, lifecycle.contextCancels)
	}
}

func TestExplicitCancelPersistsUserReasonBeforeStoppingAnalyzer(t *testing.T) {
	lifecycle := newCancellationLifecycle()
	analyzer := newBlockingAnalyzer()
	workflow, err := NewWorkflow(lifecycle, analyzer)
	if err != nil {
		t.Fatal(err)
	}
	startedResult := make(chan RunResponse, 1)
	startedError := make(chan error, 1)
	go func() {
		response, startErr := workflow.Start(context.Background(), StartCommand{TenantID: "alice", UserID: "alice", ClientRequestID: "request-2", RequestID: "request-2", TraceID: "trace-2", Message: "symptom"})
		startedResult <- response
		startedError <- startErr
	}()
	awaitStarted(t, analyzer.started)
	cancelled, err := workflow.Cancel(context.Background(), "run-cancel-1", "alice")
	if err != nil {
		t.Fatal(err)
	}
	response := awaitResponse(t, startedResult)
	if startErr := <-startedError; startErr != nil {
		t.Fatal(startErr)
	}
	if cancelled.Detail.Run.TerminalReason != "USER_CANCELLED" || response.Detail.Run.TerminalReason != "USER_CANCELLED" {
		t.Fatalf("explicit cancellation reason lost: cancel=%+v start=%+v", cancelled.Detail.Run, response.Detail.Run)
	}
	if analyzer.active.Load() != 0 || lifecycle.advanceCalls != 0 || lifecycle.explicitCancels != 1 {
		t.Fatalf("explicit cancel leaked work: active=%d advances=%d explicit_cancels=%d", analyzer.active.Load(), lifecycle.advanceCalls, lifecycle.explicitCancels)
	}
}

func awaitStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("analyzer did not start")
	}
}

func awaitResponse(t *testing.T, result <-chan RunResponse) RunResponse {
	t.Helper()
	select {
	case response := <-result:
		return response
	case <-time.After(time.Second):
		t.Fatal("workflow did not converge after cancellation")
		return RunResponse{}
	}
}
