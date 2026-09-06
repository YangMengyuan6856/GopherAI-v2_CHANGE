package reliabilityeval

import (
	"GopherAI/internal/app"
	"GopherAI/internal/contract"
	"GopherAI/internal/harness"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	SchemaVersion = "agent-reliability-acceptance-v1"
	Mode          = "isolated_in_process_fault_injection"
	StreamCount   = 20
)

type Report struct {
	SchemaVersion   string             `json:"schema_version"`
	Mode            string             `json:"mode"`
	Simulation      bool               `json:"simulation"`
	GeneratedAt     time.Time          `json:"generated_at"`
	ReportSHA256    string             `json:"report_sha256"`
	Passed          bool               `json:"passed"`
	AgentRecovery   RecoveryResult     `json:"agent_recovery"`
	SSECancellation CancellationResult `json:"sse_cancellation"`
	Guardrails      []string           `json:"guardrails"`
	Limitations     []string           `json:"limitations"`
}

type RecoveryResult struct {
	InjectedFault             string  `json:"injected_fault"`
	Scenarios                 int     `json:"scenarios"`
	Recovered                 int     `json:"recovered"`
	RecoveryRate              float64 `json:"recovery_rate"`
	CheckpointRecovered       bool    `json:"checkpoint_recovered"`
	CheckpointVersionBefore   int64   `json:"checkpoint_version_before"`
	FinalStateVersion         int64   `json:"final_state_version"`
	FinalState                string  `json:"final_state"`
	DuplicateResumeExecutions int     `json:"duplicate_resume_executions"`
	StateVersionsMonotonic    bool    `json:"state_versions_monotonic"`
	StepsBeforeRestart        int     `json:"steps_before_restart"`
	StepsAfterRecovery        int     `json:"steps_after_recovery"`
	Passed                    bool    `json:"passed"`
}

type CancellationResult struct {
	InjectedFault           string  `json:"injected_fault"`
	Streams                 int     `json:"streams"`
	CancellationObserved    int     `json:"cancellation_observed"`
	PropagationSuccessRate  float64 `json:"propagation_success_rate"`
	P50PropagationMillis    float64 `json:"p50_propagation_ms"`
	P95PropagationMillis    float64 `json:"p95_propagation_ms"`
	P99PropagationMillis    float64 `json:"p99_propagation_ms"`
	PropagationBudgetMillis int64   `json:"propagation_budget_ms"`
	DuplicateFinalEvents    int     `json:"duplicate_final_events"`
	ActiveWorkersBefore     int64   `json:"active_workers_before"`
	PeakActiveWorkers       int64   `json:"peak_active_workers"`
	ActiveWorkersAfter      int64   `json:"active_workers_after"`
	GoroutinesBefore        int     `json:"goroutines_before"`
	GoroutinesAfter         int     `json:"goroutines_after"`
	ResourceConverged       bool    `json:"resource_converged"`
	Passed                  bool    `json:"passed"`
}

func Run(ctx context.Context) (Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	recovery, err := runRecovery(ctx)
	if err != nil {
		return Report{}, err
	}
	cancellation, err := runCancellation(ctx, StreamCount)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		SchemaVersion: SchemaVersion, Mode: Mode, Simulation: true,
		GeneratedAt:   time.Now().UTC(),
		Passed:        recovery.Passed && cancellation.Passed,
		AgentRecovery: recovery, SSECancellation: cancellation,
		Guardrails: []string{"isolated_repository", "same_production_harness_service", "same_production_stream_service", "no_external_model_calls", "no_tool_calls", "no_production_state_write", "bounded_500ms_cancel_gate"},
		Limitations: []string{
			"进程中断通过丢弃 Service 实例并复用同一持久仓库语义模拟，不会终止生产进程。",
			"SSE 验收使用与生产相同的 AppService Context 传播路径和隔离阻塞 Strategy，不调用外部模型。",
			"goroutine 数仅作观测；资源收敛门以隔离 Strategy 的 active worker 归零为准。",
		},
	}
	if err := FinalizeReport(&report); err != nil {
		return Report{}, err
	}
	return report, nil
}

func FinalizeReport(report *Report) error {
	if report == nil {
		return errors.New("reliability report is required")
	}
	report.ReportSHA256 = ""
	encoded, err := json.Marshal(report)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	report.ReportSHA256 = hex.EncodeToString(digest[:])
	return ValidateReport(*report)
}

func ValidateReport(report Report) error {
	if report.SchemaVersion != SchemaVersion || report.Mode != Mode || !report.Simulation || report.GeneratedAt.IsZero() ||
		!report.Passed || len(report.ReportSHA256) != 64 || !report.AgentRecovery.Passed || !report.SSECancellation.Passed ||
		report.AgentRecovery.Scenarios <= 0 || report.AgentRecovery.Recovered != report.AgentRecovery.Scenarios ||
		report.AgentRecovery.RecoveryRate != 1 || report.AgentRecovery.DuplicateResumeExecutions != 0 || !report.AgentRecovery.CheckpointRecovered ||
		!report.AgentRecovery.StateVersionsMonotonic || report.SSECancellation.Streams <= 0 ||
		report.SSECancellation.CancellationObserved != report.SSECancellation.Streams || report.SSECancellation.PropagationSuccessRate != 1 ||
		report.SSECancellation.P95PropagationMillis > float64(report.SSECancellation.PropagationBudgetMillis) ||
		report.SSECancellation.DuplicateFinalEvents != 0 || report.SSECancellation.ActiveWorkersAfter != 0 || !report.SSECancellation.ResourceConverged {
		return errors.New("reliability report contract is invalid")
	}
	expected := report.ReportSHA256
	report.ReportSHA256 = ""
	encoded, err := json.Marshal(report)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	if hex.EncodeToString(digest[:]) != expected {
		return errors.New("reliability report hash mismatch")
	}
	return nil
}

func runRecovery(ctx context.Context) (RecoveryResult, error) {
	repository := newMemoryRepository()
	clock := &fixedClock{now: time.Date(2026, 9, 6, 6, 0, 0, 0, time.UTC)}
	ids := &sequenceIDs{}
	service, _ := harness.NewService(repository, clock, ids)
	detail, _, err := service.Create(ctx, harness.CreateCommand{
		TenantID: "acceptance", UserID: "acceptance", ClientRequestID: "recovery-case-1", RequestID: "recovery-request-1", TraceID: "recovery-trace-1",
		Intent: "troubleshooting", Strategy: "diagnosis_standard", PolicyVersion: "policy-diagnostic-v1", Goal: "verify durable checkpoint recovery",
	})
	if err != nil {
		return RecoveryResult{}, err
	}
	run := detail.Run
	commands := []harness.AdvanceCommand{
		{NextState: harness.StateContextReady, StepID: "context-ready", StepKind: "context", ReasonCode: "CONTEXT_READY", PublicSummary: "上下文已就绪。", Checkpoint: harness.CheckpointState{Goal: "verify durable checkpoint recovery", NextAction: "plan"}},
		{NextState: harness.StatePlanned, StepID: "plan-ready", StepKind: "plan", ReasonCode: "PLAN_READY", PublicSummary: "只读计划已生成。", Checkpoint: harness.CheckpointState{Goal: "verify durable checkpoint recovery", CompletedSteps: []string{"context"}, NextAction: "execute"}},
		{NextState: harness.StateRunning, StepID: "execute-started", StepKind: "execution", ReasonCode: "EXECUTION_STARTED", PublicSummary: "开始执行只读步骤。", Checkpoint: harness.CheckpointState{Goal: "verify durable checkpoint recovery", CompletedSteps: []string{"context", "plan"}, NextAction: "await_evidence"}},
		{NextState: harness.StateWaitingUser, StepID: "await-user", StepKind: "clarification", ReasonCode: "WAITING_USER", PublicSummary: "等待补充最小证据。", Checkpoint: harness.CheckpointState{Goal: "verify durable checkpoint recovery", CompletedSteps: []string{"context", "plan"}, OpenQuestions: []string{"provide bounded log signature"}, NextAction: "resume_from_checkpoint"}},
	}
	for _, command := range commands {
		command.RunID, command.UserID, command.ExpectedState, command.ExpectedVersion = run.RunID, "acceptance", run.State, run.StateVersion
		run, err = service.Advance(ctx, command)
		if err != nil {
			return RecoveryResult{}, err
		}
	}
	before, err := service.Get(ctx, run.RunID, "acceptance")
	if err != nil {
		return RecoveryResult{}, err
	}
	// Fault injection: discard the in-memory Service process object, then build
	// a fresh one over the same durable repository.
	service = nil
	clock.now = clock.now.Add(90 * time.Second)
	restarted, _ := harness.NewService(repository, clock, ids)
	recovered, err := restarted.Get(ctx, run.RunID, "acceptance")
	if err != nil {
		return RecoveryResult{}, err
	}
	checkpointRecovered := recovered.Checkpoint != nil && recovered.Checkpoint.NextAction == "resume_from_checkpoint" && len(recovered.Checkpoint.CompletedSteps) == 2
	resume := harness.AdvanceCommand{
		RunID: run.RunID, UserID: "acceptance", ExpectedState: harness.StateWaitingUser, ExpectedVersion: recovered.Run.StateVersion,
		NextState: harness.StateContextReady, StepID: "resume-context", StepKind: "lifecycle", ReasonCode: "RESUMED_FROM_CHECKPOINT", PublicSummary: "从持久 Checkpoint 恢复。",
		Checkpoint: *recovered.Checkpoint, CommandID: "resume-command-1", CommandKind: "resume",
	}
	run, err = restarted.Advance(ctx, resume)
	if err != nil {
		return RecoveryResult{}, err
	}
	afterFirstResume, _ := restarted.Get(ctx, run.RunID, "acceptance")
	duplicate, err := restarted.Advance(ctx, resume)
	if err != nil {
		return RecoveryResult{}, err
	}
	afterDuplicate, _ := restarted.Get(ctx, run.RunID, "acceptance")
	duplicateExecutions := len(afterDuplicate.Steps) - len(afterFirstResume.Steps)
	if duplicate.StateVersion != run.StateVersion {
		duplicateExecutions++
	}
	for _, command := range []harness.AdvanceCommand{
		{NextState: harness.StatePlanned, StepID: "recovery-plan", StepKind: "plan", ReasonCode: "PLAN_RESTORED", PublicSummary: "恢复后计划已校验。"},
		{NextState: harness.StateRunning, StepID: "recovery-execute", StepKind: "execution", ReasonCode: "EXECUTION_RESUMED", PublicSummary: "仅执行尚未完成的步骤。"},
		{NextState: harness.StateSucceeded, StepID: "recovery-complete", StepKind: "lifecycle", ReasonCode: "RECOVERY_COMPLETED", PublicSummary: "恢复路径完成且没有重复执行。", TerminalReason: "COMPLETED"},
	} {
		command.RunID, command.UserID, command.ExpectedState, command.ExpectedVersion = run.RunID, "acceptance", run.State, run.StateVersion
		command.Checkpoint = harness.CheckpointState{Goal: "verify durable checkpoint recovery", CompletedSteps: []string{"context", "plan", "recovery"}, NextAction: "done"}
		run, err = restarted.Advance(ctx, command)
		if err != nil {
			return RecoveryResult{}, err
		}
	}
	final, _ := restarted.Get(ctx, run.RunID, "acceptance")
	monotonic := true
	for index, step := range final.Steps {
		if step.StateVersion != int64(index+1) {
			monotonic = false
		}
	}
	result := RecoveryResult{
		InjectedFault: "service_process_restart_after_waiting_user_checkpoint", Scenarios: 1, CheckpointRecovered: checkpointRecovered,
		CheckpointVersionBefore: recovered.Run.StateVersion, FinalStateVersion: final.Run.StateVersion, FinalState: string(final.Run.State),
		DuplicateResumeExecutions: duplicateExecutions, StateVersionsMonotonic: monotonic, StepsBeforeRestart: len(before.Steps), StepsAfterRecovery: len(final.Steps),
	}
	if checkpointRecovered && final.Run.State == harness.StateSucceeded && duplicateExecutions == 0 && monotonic {
		result.Recovered = 1
	}
	result.RecoveryRate = float64(result.Recovered) / float64(result.Scenarios)
	result.Passed = result.Recovered == result.Scenarios
	return result, nil
}

type fixedClock struct{ now time.Time }

func (clock *fixedClock) Now() time.Time { return clock.now }

type sequenceIDs struct{ next atomic.Int64 }

func (ids *sequenceIDs) NewID() string { return fmt.Sprintf("recovery-run-%d", ids.next.Add(1)) }

type memoryRepository struct {
	mu          sync.Mutex
	runs        map[string]harness.Run
	requestKeys map[string]string
	steps       map[string][]harness.PublicStep
	checkpoints map[string][]harness.CheckpointState
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{runs: map[string]harness.Run{}, requestKeys: map[string]string{}, steps: map[string][]harness.PublicStep{}, checkpoints: map[string][]harness.CheckpointState{}}
}

func (repository *memoryRepository) CreateIdempotent(_ context.Context, run harness.Run, step harness.PublicStep, checkpoint harness.CheckpointState) (harness.Run, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	key := run.TenantIDHash + ":" + run.UserIDHash + ":" + run.ClientRequestID
	if runID, exists := repository.requestKeys[key]; exists {
		return repository.runs[runID], false, nil
	}
	repository.requestKeys[key], repository.runs[run.RunID] = run.RunID, run
	repository.steps[run.RunID] = []harness.PublicStep{step}
	repository.checkpoints[run.RunID] = []harness.CheckpointState{checkpoint}
	return run, true, nil
}

func (repository *memoryRepository) GetOwned(_ context.Context, runID, userIDHash string) (harness.Run, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	run, exists := repository.runs[runID]
	if !exists || run.UserIDHash != userIDHash {
		return harness.Run{}, harness.ErrRunNotFound
	}
	return run, nil
}

func (repository *memoryRepository) ListSteps(_ context.Context, runID string) ([]harness.PublicStep, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]harness.PublicStep(nil), repository.steps[runID]...), nil
}

func (repository *memoryRepository) LatestCheckpoint(_ context.Context, runID string) (*harness.CheckpointState, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	items := repository.checkpoints[runID]
	if len(items) == 0 {
		return nil, nil
	}
	item := items[len(items)-1]
	return &item, nil
}

func (repository *memoryRepository) TransitionCAS(_ context.Context, transition harness.Transition) (harness.Run, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	run, exists := repository.runs[transition.RunID]
	if !exists || run.UserIDHash != transition.UserIDHash {
		return harness.Run{}, harness.ErrRunNotFound
	}
	if run.State != transition.ExpectedState || run.StateVersion != transition.ExpectedVersion {
		if transition.CommandID != "" && run.LastCommandID == transition.CommandID && run.LastCommandKind == transition.CommandKind {
			return run, nil
		}
		return harness.Run{}, harness.ErrRunConflict
	}
	if err := harness.ValidateTransition(run.State, transition.NextState); err != nil {
		return harness.Run{}, err
	}
	run.State, run.StateVersion, run.CurrentStepID = transition.NextState, run.StateVersion+1, transition.Step.StepID
	run.NeedsUserInput, run.TerminalReason, run.LastErrorCode = transition.NextState == harness.StateWaitingUser, transition.TerminalReason, transition.ErrorCode
	if transition.CommandID != "" {
		run.LastCommandID, run.LastCommandKind = transition.CommandID, transition.CommandKind
	}
	run.Budget.UsedIterations += transition.BudgetDelta.Iterations
	run.Budget.UsedToolCalls += transition.BudgetDelta.ToolCalls
	run.Budget.UsedInputTokens += transition.BudgetDelta.InputTokens
	run.Budget.UsedOutputTokens += transition.BudgetDelta.OutputTokens
	run.Budget.UsedCostMicros += transition.BudgetDelta.CostMicros
	run.UpdatedAt = transition.At
	if transition.DeadlineExtension > 0 {
		run.DeadlineAt = run.DeadlineAt.Add(transition.DeadlineExtension)
	}
	transition.Step.StateVersion = run.StateVersion
	if harness.IsTerminal(run.State) {
		run.FinishedAt = &transition.At
	}
	repository.runs[run.RunID] = run
	repository.steps[run.RunID] = append(repository.steps[run.RunID], transition.Step)
	repository.checkpoints[run.RunID] = append(repository.checkpoints[run.RunID], transition.Checkpoint)
	return run, nil
}

type fixedSelector struct{}

func (fixedSelector) Select(context.Context, contract.RequestContext, contract.IntentResult) (contract.StrategyDecision, error) {
	return contract.StrategyDecision{
		StrategyName: "cancel_probe", StrategyVersion: "cancel-probe-v1", PolicyVersion: "reliability-fixture-v1", ReasonCode: "isolated_cancel_probe",
		Budgets: contract.ExecutionBudgets{MaxAgents: 1, MaxIterations: 1, MaxInputTokens: 128, MaxOutputTokens: 16, TotalTimeout: 5 * time.Second},
	}, nil
}

type cancelProbeStrategy struct {
	started    chan struct{}
	active     atomic.Int64
	peak       atomic.Int64
	cancelSeen atomic.Int64
	finals     atomic.Int64
}

func newCancelProbeStrategy(count int) *cancelProbeStrategy {
	return &cancelProbeStrategy{started: make(chan struct{}, count)}
}
func (*cancelProbeStrategy) Name() string { return "cancel_probe" }
func (*cancelProbeStrategy) Execute(context.Context, contract.RequestContext) (contract.AgentResult, error) {
	return contract.AgentResult{}, errors.New("cancel probe requires stream execution")
}
func (strategy *cancelProbeStrategy) Stream(ctx context.Context, _ contract.RequestContext, emit app.StreamEmitter) (contract.AgentResult, error) {
	active := strategy.active.Add(1)
	for {
		peak := strategy.peak.Load()
		if active <= peak || strategy.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	defer strategy.active.Add(-1)
	strategy.started <- struct{}{}
	if err := emit(contract.StreamEvent{Type: contract.StreamEventMeta}); err != nil {
		return contract.AgentResult{}, err
	}
	<-ctx.Done()
	strategy.cancelSeen.Add(1)
	return contract.AgentResult{}, ctx.Err()
}

func runCancellation(parent context.Context, count int) (CancellationResult, error) {
	if count < 1 || count > 100 {
		return CancellationResult{}, errors.New("stream count must be between 1 and 100")
	}
	strategy := newCancelProbeStrategy(count)
	service, err := app.NewService(fixedSelector{}, app.SystemClock{}, app.UUIDGenerator{}, strategy)
	if err != nil {
		return CancellationResult{}, err
	}
	goroutinesBefore := runtime.NumGoroutine()
	contexts := make([]context.CancelFunc, 0, count)
	results := make(chan error, count)
	finished := make(chan time.Duration, count)
	for index := 0; index < count; index++ {
		ctx, cancel := context.WithCancel(parent)
		contexts = append(contexts, cancel)
		go func(item int) {
			_, streamErr := service.Stream(ctx, app.ChatInput{UserID: "acceptance", TenantID: "acceptance", Question: fmt.Sprintf("cancel probe %d", item)}, func(event contract.StreamEvent) error {
				if event.Type == contract.StreamEventFinal {
					strategy.finals.Add(1)
				}
				return nil
			})
			results <- streamErr
		}(index)
	}
	startDeadline := time.NewTimer(time.Second)
	defer startDeadline.Stop()
	for index := 0; index < count; index++ {
		select {
		case <-strategy.started:
		case <-startDeadline.C:
			return CancellationResult{}, errors.New("not all cancellation probes started")
		case <-parent.Done():
			return CancellationResult{}, parent.Err()
		}
	}
	cancelledAt := time.Now()
	for _, cancel := range contexts {
		cancel()
	}
	go func() {
		for index := 0; index < count; index++ {
			<-results
			finished <- time.Since(cancelledAt)
		}
	}()
	durations := make([]time.Duration, 0, count)
	completionDeadline := time.NewTimer(2 * time.Second)
	defer completionDeadline.Stop()
	for len(durations) < count {
		select {
		case duration := <-finished:
			durations = append(durations, duration)
		case <-completionDeadline.C:
			return CancellationResult{}, errors.New("cancellation probes did not converge")
		case <-parent.Done():
			return CancellationResult{}, parent.Err()
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	activeAfter := strategy.active.Load()
	result := CancellationResult{
		InjectedFault: "client_sse_disconnect", Streams: count, CancellationObserved: int(strategy.cancelSeen.Load()),
		P50PropagationMillis: milliseconds(percentile(durations, .50)), P95PropagationMillis: milliseconds(percentile(durations, .95)), P99PropagationMillis: milliseconds(percentile(durations, .99)),
		PropagationBudgetMillis: 500, DuplicateFinalEvents: int(strategy.finals.Load()), ActiveWorkersBefore: 0, PeakActiveWorkers: strategy.peak.Load(), ActiveWorkersAfter: activeAfter,
		GoroutinesBefore: goroutinesBefore, GoroutinesAfter: runtime.NumGoroutine(), ResourceConverged: activeAfter == 0,
	}
	result.PropagationSuccessRate = float64(result.CancellationObserved) / float64(count)
	result.Passed = result.CancellationObserved == count && result.P95PropagationMillis <= float64(result.PropagationBudgetMillis) && result.DuplicateFinalEvents == 0 && result.ResourceConverged
	return result, nil
}

func percentile(values []time.Duration, quantile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values))*quantile+.999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func milliseconds(value time.Duration) float64 { return float64(value.Microseconds()) / 1000 }
