package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeMCPTextInvoker struct {
	toolName string
	args     map[string]any
	payload  []byte
	err      error
	calls    int
}

func (invoker *fakeMCPTextInvoker) InvokeText(_ context.Context, toolName string, args map[string]any) ([]byte, error) {
	invoker.calls++
	invoker.toolName, invoker.args = toolName, args
	return invoker.payload, invoker.err
}

func validMCPManifestPayload() []byte {
	return []byte(`{"release_id":"release-1","branch":"add_eico","git_sha":"abc123","source_dirty":false,"built_at":"2026-09-05T00:00:00Z","build_strategy":"local-cross-compile","target":"linux/amd64","go_version":"go1.25","included_components":["backend","mcp"],"config_included":false,"migrations":[],"rollback":"previous"}`)
}

func TestMCPDeploymentAdapterUsesFixedRemoteNameAndValidatesPayload(t *testing.T) {
	invoker := &fakeMCPTextInvoker{payload: validMCPManifestPayload()}
	tool := NewMCPDeploymentEvidenceToolWithInvoker(invoker)
	output, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	manifest := output.Data.(PublicDeploymentManifest)
	if invoker.toolName != mcpManifestSourceName || len(invoker.args) != 0 || manifest.ReleaseID != "release-1" || len(output.EvidenceRefs) != 1 || output.EvidenceRefs[0] != "mcp:deployment_manifest_source:release-1" {
		t.Fatalf("unexpected adapter output: invoker=%+v output=%+v", invoker, output)
	}
}

func TestMCPDeploymentAdapterRejectsUnknownRemoteFields(t *testing.T) {
	payload := strings.Replace(string(validMCPManifestPayload()), `,"rollback":"previous"`, `,"rollback":"previous","secret":"leak"`, 1)
	_, err := NewMCPDeploymentEvidenceToolWithInvoker(&fakeMCPTextInvoker{payload: []byte(payload)}).Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("unknown MCP payload field was accepted")
	}
}

func TestMCPAdapterCannotBypassRuntimePermission(t *testing.T) {
	invoker := &fakeMCPTextInvoker{payload: validMCPManifestPayload()}
	tool := NewMCPDeploymentEvidenceToolWithInvoker(invoker)
	registry := NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(registry, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	message := runtime.Invoke(context.Background(), Invocation{CallID: "mcp-eval", ToolName: tool.Definition().Name, Arguments: json.RawMessage(`{}`), Intent: "tool_task", Principal: Principal{Permissions: map[string]bool{}}, AllowedSideEffect: SideEffectReadOnly, Budget: CallBudget{MaxCalls: 1}})
	if message.ErrorCode != ErrorPermissionDenied || invoker.calls != 0 {
		t.Fatalf("MCP permission bypass: message=%+v calls=%d", message, invoker.calls)
	}
}

func TestMCPTransportFailureRemainsRetryableToRuntime(t *testing.T) {
	output, err := NewMCPDeploymentEvidenceToolWithInvoker(&fakeMCPTextInvoker{err: errors.New("protocol down")}).Execute(context.Background(), map[string]any{})
	if err == nil || !output.Retryable {
		t.Fatalf("transport failure was not retryable: output=%+v err=%v", output, err)
	}
}
