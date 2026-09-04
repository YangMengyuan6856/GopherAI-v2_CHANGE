package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type testTool struct {
	definition Definition
	execute    func(context.Context, map[string]any) (Output, error)
	calls      int
}

func (tool *testTool) Definition() Definition { return tool.definition }
func (tool *testTool) Execute(ctx context.Context, args map[string]any) (Output, error) {
	tool.calls++
	return tool.execute(ctx, args)
}

type captureAuditor struct {
	mu       sync.Mutex
	messages []ToolMessage
	err      error
}

func (auditor *captureAuditor) Record(_ context.Context, _ Invocation, message ToolMessage) error {
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	auditor.messages = append(auditor.messages, message)
	return auditor.err
}

type captureObserver struct {
	validations   []string
	calls         []string
	cancellations []string
	auditFailures int
	retries       []string
	cache         []string
	circuits      []string
}

func (observer *captureObserver) RecordToolValidation(_ string, result string) {
	observer.validations = append(observer.validations, result)
}
func (observer *captureObserver) RecordToolCall(_, _, status string, _ time.Duration) {
	observer.calls = append(observer.calls, status)
}
func (observer *captureObserver) RecordToolCancellation(_ string, reason string) {
	observer.cancellations = append(observer.cancellations, reason)
}
func (observer *captureObserver) RecordToolAuditFailure(string) { observer.auditFailures++ }
func (observer *captureObserver) RecordToolRetry(_ string, reason string) {
	observer.retries = append(observer.retries, reason)
}
func (observer *captureObserver) RecordToolCache(_ string, result string) {
	observer.cache = append(observer.cache, result)
}
func (observer *captureObserver) SetToolCircuitState(_ string, state string) {
	observer.circuits = append(observer.circuits, state)
}

func validTestDefinition() Definition {
	return Definition{
		Name: "deployment_manifest_lookup", Version: "1.0.0", Description: "test governed manifest tool",
		InputSchema: InputSchema{Type: "object", Properties: map[string]PropertySchema{
			"format": {Type: "string", Enum: []string{"summary", "full"}, MinLength: 4, MaxLength: 7},
		}, Required: []string{"format"}, AdditionalProperties: false},
		AllowedIntents: []string{"tool_task"}, RequiredPermission: "devsupport:tools:read",
		SideEffect: SideEffectReadOnly, TimeoutMS: 50, MaxResultBytes: 1024,
		Idempotent: true, RetryMaxAttempts: 1,
	}
}

func validInvocation() Invocation {
	return Invocation{
		CallID: "call-1", ToolName: "deployment_manifest_lookup", Arguments: json.RawMessage(`{"format":"summary"}`),
		Intent: "tool_task", Strategy: "tool_primary",
		Principal:         Principal{TenantID: "tenant-secret", UserID: "user-secret", Permissions: map[string]bool{"devsupport:tools:read": true}},
		AllowedSideEffect: SideEffectReadOnly, Budget: CallBudget{MaxCalls: 1},
	}
}

func newTestRuntime(t *testing.T, tool *testTool, auditor Auditor, observer Observer) *Runtime {
	t.Helper()
	registry := NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	runtime, err := NewRuntime(registry, auditor, observer)
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	return runtime
}

func TestRuntimeSuccessfulInvocationIsNormalizedAndAudited(t *testing.T) {
	tool := &testTool{definition: validTestDefinition(), execute: func(_ context.Context, args map[string]any) (Output, error) {
		return Output{Data: map[string]any{"format": args["format"], "secret": false}, EvidenceRefs: []string{"release-manifest:r1"}}, nil
	}}
	auditor, observer := &captureAuditor{}, &captureObserver{}
	runtime := newTestRuntime(t, tool, auditor, observer)
	message := runtime.Invoke(context.Background(), validInvocation())
	if message.Status != StatusSuccess || message.ErrorCode != "" || message.ToolVersion != "1.0.0" {
		t.Fatalf("unexpected message: %+v", message)
	}
	if len(message.ArgsHash) != 64 || string(message.Data) != `{"format":"summary","secret":false}` {
		t.Fatalf("unexpected normalized payload: %+v", message)
	}
	if tool.calls != 1 || len(auditor.messages) != 1 || len(observer.calls) != 1 {
		t.Fatalf("execution/audit counters mismatch")
	}
	if len(observer.validations) != 1 || observer.validations[0] != "accepted" {
		t.Fatalf("unexpected validation observations: %v", observer.validations)
	}
}

func TestRuntimeGovernanceRejectsBeforeExecution(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*Invocation)
		status     string
		errorCode  string
		validation string
	}{
		{name: "unknown argument", mutate: func(call *Invocation) { call.Arguments = json.RawMessage(`{"format":"summary","path":"/etc/passwd"}`) }, status: StatusInvalidArgs, errorCode: ErrorArgumentsInvalid, validation: "invalid_arguments"},
		{name: "wrong type", mutate: func(call *Invocation) { call.Arguments = json.RawMessage(`{"format":7}`) }, status: StatusInvalidArgs, errorCode: ErrorArgumentsInvalid, validation: "invalid_arguments"},
		{name: "intent denied", mutate: func(call *Invocation) { call.Intent = "general" }, status: StatusRejected, errorCode: ErrorIntentDenied, validation: "intent_denied"},
		{name: "permission denied", mutate: func(call *Invocation) { call.Principal.Permissions = map[string]bool{} }, status: StatusRejected, errorCode: ErrorPermissionDenied, validation: "permission_denied"},
		{name: "side effect denied", mutate: func(call *Invocation) { call.AllowedSideEffect = "" }, status: StatusRejected, errorCode: ErrorSideEffectDenied, validation: "side_effect_denied"},
		{name: "budget exhausted", mutate: func(call *Invocation) { call.Budget.UsedCalls = 1 }, status: StatusBudgetExceeded, errorCode: ErrorBudgetExceeded, validation: "budget_exceeded"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tool := &testTool{definition: validTestDefinition(), execute: func(context.Context, map[string]any) (Output, error) { return Output{}, nil }}
			auditor, observer := &captureAuditor{}, &captureObserver{}
			runtime := newTestRuntime(t, tool, auditor, observer)
			call := validInvocation()
			testCase.mutate(&call)
			message := runtime.Invoke(context.Background(), call)
			if message.Status != testCase.status || message.ErrorCode != testCase.errorCode {
				t.Fatalf("unexpected message: %+v", message)
			}
			if tool.calls != 0 || len(auditor.messages) != 1 {
				t.Fatalf("rejected call executed or was not audited")
			}
			if len(observer.validations) != 1 || observer.validations[0] != testCase.validation {
				t.Fatalf("unexpected validations: %v", observer.validations)
			}
		})
	}
}

func TestRuntimeNeverFuzzyMatchesUnknownTool(t *testing.T) {
	tool := &testTool{definition: validTestDefinition(), execute: func(context.Context, map[string]any) (Output, error) { return Output{}, nil }}
	auditor, observer := &captureAuditor{}, &captureObserver{}
	runtime := newTestRuntime(t, tool, auditor, observer)
	call := validInvocation()
	call.ToolName = "deployment_manifest_looku"
	message := runtime.Invoke(context.Background(), call)
	if message.ErrorCode != ErrorToolNotRegistered || tool.calls != 0 || len(auditor.messages) != 1 {
		t.Fatalf("unknown name was not safely rejected: %+v", message)
	}
}

func TestRuntimeActionGuardStopsRepeatedCanonicalAction(t *testing.T) {
	tool := &testTool{definition: validTestDefinition(), execute: func(context.Context, map[string]any) (Output, error) {
		return Output{Data: map[string]any{"ok": true}}, nil
	}}
	auditor, observer := &captureAuditor{}, &captureObserver{}
	runtime := newTestRuntime(t, tool, auditor, observer)
	guard := NewActionGuard()
	firstCall := validInvocation()
	firstCall.Budget.MaxCalls = 2
	firstCall.ActionGuard = guard
	first := runtime.Invoke(context.Background(), firstCall)
	secondCall := validInvocation()
	secondCall.CallID = "call-2"
	secondCall.Arguments = json.RawMessage("{\n  \"format\": \"summary\"\n}")
	secondCall.Budget = CallBudget{MaxCalls: 2, UsedCalls: 1}
	secondCall.ActionGuard = guard
	second := runtime.Invoke(context.Background(), secondCall)
	if first.Status != StatusSuccess || second.Status != StatusNoProgress || second.ErrorCode != ErrorNoProgress {
		t.Fatalf("unexpected guarded results: first=%+v second=%+v", first, second)
	}
	if first.ArgsHash != second.ArgsHash || tool.calls != 1 || len(auditor.messages) != 2 {
		t.Fatalf("duplicate action executed or was not audited: calls=%d audits=%d", tool.calls, len(auditor.messages))
	}
	if len(observer.validations) != 2 || observer.validations[0] != "accepted" || observer.validations[1] != "no_progress" {
		t.Fatalf("unexpected validation observations: %v", observer.validations)
	}
}

func TestRuntimeActionGuardAllowsDifferentCanonicalArguments(t *testing.T) {
	tool := &testTool{definition: validTestDefinition(), execute: func(context.Context, map[string]any) (Output, error) {
		return Output{Data: map[string]any{"ok": true}}, nil
	}}
	runtime := newTestRuntime(t, tool, &captureAuditor{}, &captureObserver{})
	guard := NewActionGuard()
	first := validInvocation()
	first.Budget.MaxCalls, first.ActionGuard = 2, guard
	second := validInvocation()
	second.CallID = "call-2"
	second.Arguments = json.RawMessage(`{"format":"full"}`)
	second.Budget, second.ActionGuard = CallBudget{MaxCalls: 2, UsedCalls: 1}, guard
	if result := runtime.Invoke(context.Background(), first); result.Status != StatusSuccess {
		t.Fatalf("first action failed: %+v", result)
	}
	if result := runtime.Invoke(context.Background(), second); result.Status != StatusSuccess {
		t.Fatalf("different action was blocked: %+v", result)
	}
	if tool.calls != 2 {
		t.Fatalf("expected two distinct executions, got %d", tool.calls)
	}
}

func TestRuntimeTimeoutAndAuditFailureAreObservable(t *testing.T) {
	tool := &testTool{definition: validTestDefinition(), execute: func(ctx context.Context, _ map[string]any) (Output, error) {
		<-ctx.Done()
		return Output{}, ctx.Err()
	}}
	auditor, observer := &captureAuditor{err: errors.New("database unavailable")}, &captureObserver{}
	runtime := newTestRuntime(t, tool, auditor, observer)
	message := runtime.Invoke(context.Background(), validInvocation())
	if message.Status != StatusTimeout || message.ErrorCode != ErrorTimeout || !message.Retryable {
		t.Fatalf("unexpected timeout message: %+v", message)
	}
	if observer.auditFailures != 1 || len(observer.cancellations) != 1 || observer.cancellations[0] != "timeout" {
		t.Fatalf("timeout/audit failure not observed: %+v", observer)
	}
}

func TestRegistryRejectsDuplicateAndReturnsSortedDefinitions(t *testing.T) {
	registry := NewRegistry()
	first := &testTool{definition: validTestDefinition(), execute: func(context.Context, map[string]any) (Output, error) { return Output{}, nil }}
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(registry.Register(first), ErrToolAlreadyRegistered) {
		t.Fatal("duplicate registration must fail")
	}
	definitions := registry.Definitions()
	if len(definitions) != 1 || definitions[0].Name != "deployment_manifest_lookup" {
		t.Fatalf("unexpected definitions: %+v", definitions)
	}
}

func TestRuntimeRetriesOnlyRetryableIdempotentFailureAndThenCaches(t *testing.T) {
	tool := &testTool{definition: validTestDefinition()}
	tool.definition.RetryMaxAttempts = 2
	tool.definition.CacheTTLMS = 1000
	tool.execute = func(context.Context, map[string]any) (Output, error) {
		if tool.calls == 1 {
			return Output{Retryable: true}, errors.New("temporary")
		}
		return Output{Data: map[string]any{"ok": true}}, nil
	}
	observer := &captureObserver{}
	runtime := newTestRuntime(t, tool, &captureAuditor{}, observer)
	first := runtime.Invoke(context.Background(), validInvocation())
	secondCall := validInvocation()
	secondCall.CallID = "call-2"
	second := runtime.Invoke(context.Background(), secondCall)
	if first.Status != StatusSuccess || first.Cached || second.Status != StatusSuccess || !second.Cached {
		t.Fatalf("unexpected retry/cache results: first=%+v second=%+v", first, second)
	}
	if tool.calls != 2 || len(observer.retries) != 1 || observer.retries[0] != "temporary_error" {
		t.Fatalf("unexpected executions/retries: calls=%d observer=%+v", tool.calls, observer)
	}
	if len(observer.cache) != 2 || observer.cache[0] != "miss" || observer.cache[1] != "hit" {
		t.Fatalf("unexpected cache sequence: %v", observer.cache)
	}
}

func TestRuntimeCircuitOpensAndHalfOpenProbeRecovers(t *testing.T) {
	tool := &testTool{definition: validTestDefinition()}
	tool.definition.CircuitFailures = 2
	tool.definition.CircuitOpenMS = 1000
	shouldFail := true
	tool.execute = func(context.Context, map[string]any) (Output, error) {
		if shouldFail {
			return Output{}, errors.New("dependency down")
		}
		return Output{Data: map[string]any{"ok": true}}, nil
	}
	observer := &captureObserver{}
	runtime := newTestRuntime(t, tool, &captureAuditor{}, observer)
	now := time.Date(2026, 9, 5, 5, 30, 0, 0, time.UTC)
	runtime.now = func() time.Time { return now }
	if result := runtime.Invoke(context.Background(), validInvocation()); result.ErrorCode != ErrorExecutionFailed {
		t.Fatalf("first failure: %+v", result)
	}
	if result := runtime.Invoke(context.Background(), validInvocation()); result.ErrorCode != ErrorExecutionFailed {
		t.Fatalf("second failure: %+v", result)
	}
	if result := runtime.Invoke(context.Background(), validInvocation()); result.ErrorCode != ErrorCircuitOpen {
		t.Fatalf("open circuit did not fail fast: %+v", result)
	}
	if tool.calls != 2 {
		t.Fatalf("open circuit executed dependency: %d", tool.calls)
	}
	now = now.Add(1100 * time.Millisecond)
	shouldFail = false
	if result := runtime.Invoke(context.Background(), validInvocation()); result.Status != StatusSuccess {
		t.Fatalf("half-open probe did not recover: %+v", result)
	}
	if len(observer.circuits) != 3 || observer.circuits[0] != "open" || observer.circuits[1] != "half_open" || observer.circuits[2] != "closed" {
		t.Fatalf("unexpected circuit transitions: %v", observer.circuits)
	}
}

func TestRuntimeStaleIfErrorIsExplicitAndCircuitAware(t *testing.T) {
	tool := &testTool{definition: validTestDefinition()}
	tool.definition.CacheTTLMS = 1000
	tool.definition.StaleIfErrorMS = 10000
	tool.definition.CircuitFailures = 1
	tool.definition.CircuitOpenMS = 5000
	shouldFail := false
	tool.execute = func(context.Context, map[string]any) (Output, error) {
		if shouldFail {
			return Output{Retryable: true}, errors.New("dependency unavailable")
		}
		return Output{Data: map[string]any{"release": "r1"}, EvidenceRefs: []string{"source:r1"}}, nil
	}
	auditor, observer := &captureAuditor{}, &captureObserver{}
	runtime := newTestRuntime(t, tool, auditor, observer)
	now := time.Date(2026, 9, 5, 6, 30, 0, 0, time.UTC)
	runtime.now = func() time.Time { return now }
	if seeded := runtime.Invoke(context.Background(), validInvocation()); seeded.Status != StatusSuccess || seeded.Cached {
		t.Fatalf("cache seed failed: %+v", seeded)
	}
	now = now.Add(1100 * time.Millisecond)
	shouldFail = true
	stale := runtime.Invoke(context.Background(), validInvocation())
	if stale.Status != StatusSuccess || !stale.Cached || !stale.Stale || stale.DegradedReason != ErrorExecutionFailed {
		t.Fatalf("dependency failure was not explicit stale fallback: %+v", stale)
	}
	if stale.ErrorCode != "" || stale.Retryable || len(stale.EvidenceRefs) != 2 || stale.EvidenceRefs[1] != "tool-cache-stale:deployment_manifest_lookup@1.0.0" {
		t.Fatalf("stale provenance is incomplete: %+v", stale)
	}
	openCircuit := runtime.Invoke(context.Background(), validInvocation())
	if !openCircuit.Stale || openCircuit.DegradedReason != ErrorCircuitOpen || tool.calls != 2 {
		t.Fatalf("open circuit did not fail over without dependency execution: message=%+v calls=%d", openCircuit, tool.calls)
	}
	if len(observer.cache) != 3 || observer.cache[0] != "miss" || observer.cache[1] != "stale_fallback" || observer.cache[2] != "stale_fallback" {
		t.Fatalf("unexpected stale cache observations: %v", observer.cache)
	}
	if len(auditor.messages) != 3 || !auditor.messages[1].Stale || auditor.messages[1].DegradedReason != ErrorExecutionFailed {
		t.Fatalf("stale result was not audited: %+v", auditor.messages)
	}
}

func TestRuntimeStaleWindowExpiryAndCallerCancellationFailClosed(t *testing.T) {
	tool := &testTool{definition: validTestDefinition()}
	tool.definition.CacheTTLMS = 100
	tool.definition.StaleIfErrorMS = 200
	shouldFail := false
	tool.execute = func(ctx context.Context, _ map[string]any) (Output, error) {
		if shouldFail {
			if err := ctx.Err(); err != nil {
				return Output{}, err
			}
			return Output{Retryable: true}, errors.New("dependency unavailable")
		}
		return Output{Data: map[string]any{"ok": true}}, nil
	}
	runtime := newTestRuntime(t, tool, &captureAuditor{}, &captureObserver{})
	now := time.Date(2026, 9, 5, 6, 45, 0, 0, time.UTC)
	runtime.now = func() time.Time { return now }
	if seeded := runtime.Invoke(context.Background(), validInvocation()); seeded.Status != StatusSuccess {
		t.Fatalf("cache seed failed: %+v", seeded)
	}
	shouldFail = true
	now = now.Add(150 * time.Millisecond)
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := runtime.Invoke(cancelledContext, validInvocation())
	if cancelled.Status != StatusCancelled || cancelled.Stale || cancelled.Cached {
		t.Fatalf("caller cancellation must not be hidden by stale data: %+v", cancelled)
	}
	now = now.Add(200 * time.Millisecond)
	expired := runtime.Invoke(context.Background(), validInvocation())
	if expired.Status != StatusError || expired.ErrorCode != ErrorExecutionFailed || expired.Stale || expired.Cached {
		t.Fatalf("expired stale entry must fail closed: %+v", expired)
	}
}

func TestRegistryRejectsUnsafeStalePolicies(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Definition)
	}{
		{name: "without cache", mutate: func(definition *Definition) { definition.StaleIfErrorMS = 1000 }},
		{name: "write tool", mutate: func(definition *Definition) {
			definition.CacheTTLMS, definition.StaleIfErrorMS, definition.SideEffect = 1000, 1000, SideEffectInternalWrite
		}},
		{name: "outside bound", mutate: func(definition *Definition) { definition.CacheTTLMS, definition.StaleIfErrorMS = 1000, 300001 }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			definition := validTestDefinition()
			testCase.mutate(&definition)
			tool := &testTool{definition: definition, execute: func(context.Context, map[string]any) (Output, error) { return Output{}, nil }}
			if err := NewRegistry().Register(tool); err == nil {
				t.Fatal("unsafe stale policy must be rejected")
			}
		})
	}
}
