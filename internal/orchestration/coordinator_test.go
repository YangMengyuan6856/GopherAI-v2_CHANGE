package orchestration

import (
	"context"
	"testing"
)

type planBuilderStub struct{ plan CollaborationPlan }

func (stub planBuilderStub) Plan(context.Context, string) (CollaborationPlan, error) {
	return stub.plan, nil
}

type planExecutorStub struct {
	result ExecutionResult
	calls  int
}

func (stub *planExecutorStub) Execute(context.Context, CollaborationPlan, ExecutionInput) (ExecutionResult, error) {
	stub.calls++
	return stub.result, nil
}

type synthesizerStub struct {
	result SynthesisResult
	calls  int
}

func (stub *synthesizerStub) Synthesize(context.Context, ExecutionResult, string) (SynthesisResult, error) {
	stub.calls++
	return stub.result, nil
}

func TestCoordinatorDoesNotExecuteSimpleRequest(t *testing.T) {
	executor, synthesizer := new(planExecutorStub), new(synthesizerStub)
	coordinator, _ := NewShadowCoordinator(planBuilderStub{plan: CollaborationPlan{Decision: DecisionSingleAgent}}, executor, synthesizer)
	result, err := coordinator.Run(context.Background(), ExecutionInput{TenantID: "alice", UserID: "alice", Message: "Redis NOAUTH"})
	if err != nil || result.Executed || executor.calls != 0 || synthesizer.calls != 0 || result.FallbackStrategy != "diagnosis_standard" {
		t.Fatalf("simple request crossed collaboration gate: result=%+v executor=%d synth=%d err=%v", result, executor.calls, synthesizer.calls, err)
	}
}

func TestCoordinatorExecutesAndSynthesizesCollaborativeCandidate(t *testing.T) {
	execution := ExecutionResult{SchemaVersion: ExecutionSchemaVersion, Mode: "shadow_only", TaskResults: []TaskExecution{}}
	synthesis := SynthesisResult{Status: SynthesisComplete, ReasonCode: "all_claims_citation_verified"}
	executor := &planExecutorStub{result: execution}
	synthesizer := &synthesizerStub{result: synthesis}
	coordinator, _ := NewShadowCoordinator(planBuilderStub{plan: CollaborationPlan{Decision: DecisionCollaborative}}, executor, synthesizer)
	result, err := coordinator.Run(context.Background(), ExecutionInput{TenantID: "alice", UserID: "alice", Message: "HTTP 502，同时核对项目文档"})
	if err != nil || !result.Executed || executor.calls != 1 || synthesizer.calls != 1 || result.Status != SynthesisComplete {
		t.Fatalf("candidate did not complete shadow collaboration: result=%+v err=%v", result, err)
	}
}
