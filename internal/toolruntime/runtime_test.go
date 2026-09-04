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

func validTestDefinition() Definition {
	return Definition{
		Name: "deployment_manifest_lookup", Version: "1.0.0", Description: "test governed manifest tool",
		InputSchema: InputSchema{Type: "object", Properties: map[string]PropertySchema{
			"format": {Type: "string", Enum: []string{"summary", "full"}, MinLength: 4, MaxLength: 7},
		}, Required: []string{"format"}, AdditionalProperties: false},
		AllowedIntents: []string{"tool_task"}, RequiredPermission: "devsupport:tools:read",
		SideEffect: SideEffectReadOnly, TimeoutMS: 50, MaxResultBytes: 1024,
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
