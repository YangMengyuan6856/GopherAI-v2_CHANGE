package toolruntime

import (
	"context"
	"encoding/json"
	"testing"

	"GopherAI/internal/harness"
	"GopherAI/internal/incident"
)

type fakeResolutionConfirmer struct {
	command incident.ConfirmCommand
	result  incident.Confirmation
	err     error
	calls   int
}

func (confirmer *fakeResolutionConfirmer) Confirm(_ context.Context, command incident.ConfirmCommand) (incident.Confirmation, error) {
	confirmer.calls++
	confirmer.command = command
	return confirmer.result, confirmer.err
}

func resolutionInvocation() Invocation {
	return Invocation{
		CallID: "confirm-call-1", TraceID: "confirm-trace", ToolName: "confirm_resolution",
		Arguments: json.RawMessage(`{"run_id":"run-1","hypothesis_id":"cause-1","resolution":"已修复 Redis 密码并验证 PONG","client_request_id":"confirm-1","expected_state_version":5}`),
		Intent:    "troubleshooting", Strategy: "human_confirmed_action_v1",
		Principal: Principal{TenantID: "tenant-a", UserID: "alice", Permissions: map[string]bool{
			"devsupport:resolution:confirm": true,
		}},
		AllowedSideEffect: SideEffectInternalWrite, Budget: CallBudget{MaxCalls: 1},
	}
}

func resolutionRuntimeForTest(t *testing.T, confirmer ResolutionConfirmer, auditor Auditor) *Runtime {
	t.Helper()
	registry := NewRegistry()
	if err := registry.Register(NewConfirmResolutionTool(confirmer)); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(registry, auditor, nil)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func TestConfirmResolutionToolUsesTrustedPrincipalAndAuditsInternalWrite(t *testing.T) {
	confirmer := &fakeResolutionConfirmer{result: incident.Confirmation{
		SchemaVersion: incident.SchemaVersion, Created: true,
		Incident: incident.PublicResolvedIncident{ID: "incident-1", SourceRunID: "run-1", Status: incident.StatusConfirmed},
	}}
	auditor := &captureAuditor{}
	runtime := resolutionRuntimeForTest(t, confirmer, auditor)
	message := runtime.Invoke(context.Background(), resolutionInvocation())
	if message.Status != StatusSuccess || message.ToolName != "confirm_resolution" || message.Cached || message.Stale {
		t.Fatalf("unexpected confirmation message: %+v", message)
	}
	if confirmer.calls != 1 || confirmer.command.UserID != "alice" || confirmer.command.ExpectedStateVersion != 5 || confirmer.command.ClientRequestID != "confirm-1" {
		t.Fatalf("trusted command was not preserved: calls=%d command=%+v", confirmer.calls, confirmer.command)
	}
	if len(message.EvidenceRefs) != 1 || message.EvidenceRefs[0] != "resolution-confirmation:incident-1" || len(auditor.messages) != 1 {
		t.Fatalf("confirmation evidence/audit missing: message=%+v audits=%d", message, len(auditor.messages))
	}
}

func TestConfirmResolutionToolCannotBypassHumanWriteBoundary(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Invocation)
		errorCode string
	}{
		{name: "permission missing", mutate: func(call *Invocation) { call.Principal.Permissions = map[string]bool{} }, errorCode: ErrorPermissionDenied},
		{name: "read only caller", mutate: func(call *Invocation) { call.AllowedSideEffect = SideEffectReadOnly }, errorCode: ErrorSideEffectDenied},
		{name: "budget exhausted", mutate: func(call *Invocation) { call.Budget.UsedCalls = 1 }, errorCode: ErrorBudgetExceeded},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			confirmer := &fakeResolutionConfirmer{}
			runtime := resolutionRuntimeForTest(t, confirmer, &captureAuditor{})
			call := resolutionInvocation()
			testCase.mutate(&call)
			message := runtime.Invoke(context.Background(), call)
			if message.ErrorCode != testCase.errorCode || confirmer.calls != 0 {
				t.Fatalf("write boundary bypassed: message=%+v calls=%d", message, confirmer.calls)
			}
		})
	}
}

func TestConfirmResolutionToolMapsDomainConflictWithoutLeakingError(t *testing.T) {
	confirmer := &fakeResolutionConfirmer{err: harness.ErrRunConflict}
	runtime := resolutionRuntimeForTest(t, confirmer, &captureAuditor{})
	message := runtime.Invoke(context.Background(), resolutionInvocation())
	if message.Status != StatusError || message.ErrorCode != ErrorResolutionStateConflict || message.Retryable || len(message.Data) != 0 {
		t.Fatalf("unexpected domain error mapping: %+v", message)
	}
}

func TestExternalWriteToolIsRejectedByInternalWriteGrant(t *testing.T) {
	tool := &testTool{definition: validTestDefinition(), execute: func(context.Context, map[string]any) (Output, error) {
		return Output{Data: map[string]any{"executed": true}}, nil
	}}
	tool.definition.Name = "external_write_probe"
	tool.definition.SideEffect = SideEffectExternalWrite
	tool.definition.Idempotent = false
	runtime := newTestRuntime(t, tool, &captureAuditor{}, &captureObserver{})
	call := validInvocation()
	call.ToolName = tool.definition.Name
	call.AllowedSideEffect = SideEffectInternalWrite
	message := runtime.Invoke(context.Background(), call)
	if message.ErrorCode != ErrorSideEffectDenied || tool.calls != 0 {
		t.Fatalf("external write must remain denied: %+v calls=%d", message, tool.calls)
	}
}
