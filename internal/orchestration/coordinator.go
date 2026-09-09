package orchestration

import (
	"context"
	"errors"
	"strings"
)

const CollaborationRunSchemaVersion = "diagnosis-collaborative-shadow-v1"

type PlanBuilder interface {
	Plan(context.Context, string) (CollaborationPlan, error)
}

type PlanExecutor interface {
	Execute(context.Context, CollaborationPlan, ExecutionInput) (ExecutionResult, error)
}

type ResultSynthesizer interface {
	Synthesize(context.Context, ExecutionResult, string) (SynthesisResult, error)
}

type CollaborationRun struct {
	SchemaVersion      string            `json:"schema_version"`
	Mode               string            `json:"mode"`
	AffectsLiveTraffic bool              `json:"affects_live_traffic"`
	Executed           bool              `json:"executed"`
	Status             string            `json:"status"`
	ReasonCode         string            `json:"reason_code"`
	FallbackStrategy   string            `json:"fallback_strategy,omitempty"`
	TraceID            string            `json:"trace_id,omitempty"`
	Plan               CollaborationPlan `json:"plan"`
	Execution          *ExecutionResult  `json:"execution,omitempty"`
	Synthesis          *SynthesisResult  `json:"synthesis,omitempty"`
}

type ShadowCoordinator struct {
	planner     PlanBuilder
	executor    PlanExecutor
	synthesizer ResultSynthesizer
}

func NewShadowCoordinator(planner PlanBuilder, executor PlanExecutor, synthesizer ResultSynthesizer) (*ShadowCoordinator, error) {
	if planner == nil || executor == nil || synthesizer == nil {
		return nil, errors.New("collaboration coordinator dependencies are required")
	}
	return &ShadowCoordinator{planner: planner, executor: executor, synthesizer: synthesizer}, nil
}

func (coordinator *ShadowCoordinator) Run(ctx context.Context, input ExecutionInput) (CollaborationRun, error) {
	result := CollaborationRun{
		SchemaVersion: CollaborationRunSchemaVersion, Mode: "shadow_only", AffectsLiveTraffic: false,
		Status: "not_executed", ReasonCode: "single_agent_gate", FallbackStrategy: "diagnosis_standard",
	}
	if coordinator == nil || coordinator.planner == nil || coordinator.executor == nil || coordinator.synthesizer == nil {
		return result, errors.New("collaboration coordinator is unavailable")
	}
	input.TenantID, input.UserID, input.Message = strings.TrimSpace(input.TenantID), strings.TrimSpace(input.UserID), strings.TrimSpace(input.Message)
	if input.TenantID == "" || input.UserID == "" || input.Message == "" {
		return result, errors.New("tenant, user and message are required")
	}
	plan, err := coordinator.planner.Plan(ctx, input.Message)
	result.Plan = plan
	if err != nil {
		return result, err
	}
	if plan.Decision != DecisionCollaborative {
		return result, nil
	}
	result.Executed, result.Status, result.ReasonCode = true, "running", "collaborative_shadow_started"
	execution, err := coordinator.executor.Execute(ctx, plan, input)
	result.Execution = &execution
	if err != nil {
		result.Status, result.ReasonCode = "failed", execution.ReasonCode
		return result, err
	}
	synthesis, err := coordinator.synthesizer.Synthesize(ctx, execution, input.TenantID)
	result.Synthesis = &synthesis
	if err != nil {
		result.Status, result.ReasonCode = "failed", "synthesis_failed"
		return result, err
	}
	result.Status, result.ReasonCode, result.FallbackStrategy = synthesis.Status, synthesis.ReasonCode, synthesis.FallbackStrategy
	return result, nil
}
