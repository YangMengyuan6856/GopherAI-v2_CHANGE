package faultcampaign

import (
	"context"
	"encoding/json"
	"errors"

	"GopherAI/internal/orchestration"
	"GopherAI/internal/rag"
	"GopherAI/internal/toolruntime"
)

func runIsolatedProbe(scenarioID string) (string, error) {
	switch scenarioID {
	case "rag_groundedness_drop":
		gate := rag.DefaultEvidenceGate().Evaluate(rag.SearchOutput{})
		if gate.Accepted || gate.ReasonCode != rag.GateReasonNoEvidence {
			return "", errors.New("isolated RAG evidence gate did not reject empty retrieval")
		}
		return "RAG_NO_EVIDENCE", nil
	case "agent_latency_spike":
		plan, err := orchestration.NewDefaultBoundedPlanner().Plan(context.Background(), "Redis NOAUTH Authentication required")
		if err != nil {
			return "", err
		}
		plan.Tasks[0].Budget.TimeoutMS = 1
		executor, err := orchestration.NewParallelExecutor(map[string]orchestration.AgentRunner{
			orchestration.DiagnosticAgentRole: blockingAgentRunner{},
		})
		if err != nil {
			return "", err
		}
		result, err := executor.Execute(context.Background(), plan, orchestration.ExecutionInput{TenantID: "fault-fixture", UserID: "fault-fixture", Message: "Redis NOAUTH Authentication required"})
		if err != nil {
			return "", err
		}
		if len(result.TaskResults) != 1 || result.TaskResults[0].Status != orchestration.TaskStatusTimedOut || result.TaskResults[0].ReasonCode != "task_timeout_exceeded" {
			return "", errors.New("isolated Agent runner did not reach the production timeout boundary")
		}
		return "AGENT_TASK_TIMEOUT", nil
	case "tool_timeout_burst":
		registry := toolruntime.NewRegistry()
		if err := registry.Register(blockingTool{}); err != nil {
			return "", err
		}
		runtime, err := toolruntime.NewRuntime(registry, nil, nil)
		if err != nil {
			return "", err
		}
		message := runtime.Invoke(context.Background(), toolruntime.Invocation{
			CallID: "fault-fixture-call", ToolName: "fault_acceptance_tool", Arguments: json.RawMessage(`{"scope":"isolated"}`),
			Intent: "tool_task", Strategy: "tool_primary", Principal: toolruntime.Principal{TenantID: "fault-fixture", UserID: "fault-fixture", Permissions: map[string]bool{"devsupport:tools:read": true}},
			AllowedSideEffect: toolruntime.SideEffectReadOnly, Budget: toolruntime.CallBudget{MaxCalls: 1},
		})
		if message.Status != toolruntime.StatusTimeout || message.ErrorCode != toolruntime.ErrorTimeout {
			return "", errors.New("isolated tool adapter did not reach the governed timeout boundary")
		}
		return message.ErrorCode, nil
	default:
		return "", errors.New("unknown isolated fault probe")
	}
}

type blockingAgentRunner struct{}

func (blockingAgentRunner) Run(ctx context.Context, _ orchestration.PlannedTask, _ orchestration.ExecutionInput) (orchestration.AgentOutput, error) {
	<-ctx.Done()
	return orchestration.AgentOutput{}, ctx.Err()
}

type blockingTool struct{}

func (blockingTool) Definition() toolruntime.Definition {
	return toolruntime.Definition{
		Name: "fault_acceptance_tool", Version: "1.0.0", Description: "isolated timeout acceptance adapter",
		InputSchema: toolruntime.InputSchema{Type: "object", Properties: map[string]toolruntime.PropertySchema{
			"scope": {Type: "string", Enum: []string{"isolated"}, MinLength: 8, MaxLength: 8},
		}, Required: []string{"scope"}, AdditionalProperties: false},
		AllowedIntents: []string{"tool_task"}, RequiredPermission: "devsupport:tools:read", SideEffect: toolruntime.SideEffectReadOnly,
		TimeoutMS: 10, MaxResultBytes: 1024, Idempotent: true, RetryMaxAttempts: 1,
	}
}

func (blockingTool) Execute(ctx context.Context, _ map[string]any) (toolruntime.Output, error) {
	<-ctx.Done()
	return toolruntime.Output{}, ctx.Err()
}
