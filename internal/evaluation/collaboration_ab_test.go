package evaluation

import (
	"context"
	"os"
	"testing"
	"time"

	"GopherAI/internal/diagnostic"
	"GopherAI/internal/orchestration"
)

type collaborationRunnerStub struct {
	runs map[string]orchestration.CollaborationRun
}

func (stub collaborationRunnerStub) Run(_ context.Context, input orchestration.ExecutionInput) (orchestration.CollaborationRun, error) {
	return stub.runs[input.Message], nil
}

func TestCollaborationABReportsPairedBenefitWithoutPromotingPendingLabels(t *testing.T) {
	cases := loadVersionedCollaborationCases(t)
	runs := make(map[string]orchestration.CollaborationRun, len(cases))
	for _, item := range cases {
		if item.Slice == CollaborationSliceGuard {
			runs[item.Question] = guardedCollaborationRun(t, item.Question)
			continue
		}
		runs[item.Question] = successfulCollaborationRun(t, item)
	}
	report, err := EvaluateCollaborationAB(context.Background(), cases, "tenant", "user", "candidate", time.Unix(1, 0), diagnostic.NewAgent(), collaborationRunnerStub{runs: runs})
	if err != nil {
		t.Fatal(err)
	}
	if !report.TechnicalGatesPassed || !report.NetBenefitPassed {
		t.Fatalf("expected technical and net-benefit gates to pass: %+v", report.GateFailures)
	}
	if report.HumanReviewed || report.BaselineEligible || report.PromotionEligible || report.DefaultTrafficEnabled || report.RecommendedDefaultWeight != 0 {
		t.Fatalf("pending labels must not promote candidate: %+v", report)
	}
	if report.Metrics.SimpleFalseTriggerRate != 0 || report.Metrics.TargetTriggerRate != 1 || report.Metrics.MaximumObservedAgents != 2 {
		t.Fatalf("unexpected routing metrics: %+v", report.Metrics)
	}
	if report.Metrics.BaselineMeanQuality != .6 || report.Metrics.CandidateMeanQuality != 1 || report.Metrics.MeanQualityDelta != .4 {
		t.Fatalf("unexpected paired quality metrics: %+v", report.Metrics)
	}
	if report.Metrics.QualityDeltaCI95Lower < .03 {
		t.Fatalf("quality confidence interval must clear the G7 threshold: %+v", report.Metrics)
	}
}

func TestCollaborationDatasetSimpleCasesRemainBelowPlannerGate(t *testing.T) {
	planner := orchestration.NewDefaultBoundedPlanner()
	for _, item := range loadVersionedCollaborationCases(t) {
		plan, err := planner.Plan(context.Background(), item.Question)
		if err != nil {
			t.Fatalf("plan %s: %v", item.ID, err)
		}
		if item.Slice == CollaborationSliceTarget && plan.Decision != orchestration.DecisionCollaborative {
			t.Errorf("target %s did not trigger collaboration: score=%d reason=%s", item.ID, plan.ComplexityScore, plan.ReasonCode)
		}
		if item.Slice == CollaborationSliceGuard && plan.Decision != orchestration.DecisionSingleAgent {
			t.Errorf("simple guard %s mis-triggered collaboration: score=%d reason=%s", item.ID, plan.ComplexityScore, plan.ReasonCode)
		}
	}
}

func TestCollaborationABFailsClosedOnSimpleMisTrigger(t *testing.T) {
	cases := loadVersionedCollaborationCases(t)
	runs := make(map[string]orchestration.CollaborationRun, len(cases))
	for _, item := range cases {
		if item.Slice == CollaborationSliceTarget {
			runs[item.Question] = successfulCollaborationRun(t, item)
		} else {
			runs[item.Question] = guardedCollaborationRun(t, item.Question)
		}
	}
	firstGuard := cases[CollaborationTargetCaseCount]
	triggered := successfulCollaborationRun(t, CollaborationCase{Expected: CollaborationExpected{EvidenceIDs: []string{"guard-evidence"}, RootCauseIDs: []string{"guard-root"}}})
	runs[firstGuard.Question] = triggered
	report, err := EvaluateCollaborationAB(context.Background(), cases, "tenant", "user", "candidate", time.Unix(1, 0), diagnostic.NewAgent(), collaborationRunnerStub{runs: runs})
	if err != nil {
		t.Fatal(err)
	}
	if report.TechnicalGatesPassed || report.Metrics.SimpleFalseTriggerRate != .1 || !containsReason(report.GateFailures, "simple_false_trigger_rate_nonzero") {
		t.Fatalf("simple mis-trigger must fail the gate: %+v", report)
	}
}

func loadVersionedCollaborationCases(t *testing.T) []CollaborationCase {
	t.Helper()
	file, err := os.Open("../../evals/devsupport-collaboration-ab-v1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	cases, err := LoadCollaborationCases(file)
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

func guardedCollaborationRun(t *testing.T, message string) orchestration.CollaborationRun {
	t.Helper()
	planner := orchestration.NewDefaultBoundedPlanner()
	plan, err := planner.Plan(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	return orchestration.CollaborationRun{
		SchemaVersion: orchestration.CollaborationRunSchemaVersion, Mode: "shadow_only", AffectsLiveTraffic: false,
		Executed: false, Status: "not_executed", ReasonCode: "single_agent_gate", FallbackStrategy: "diagnosis_standard", Plan: plan,
	}
}

func successfulCollaborationRun(t *testing.T, item CollaborationCase) orchestration.CollaborationRun {
	t.Helper()
	planner := orchestration.NewDefaultBoundedPlanner()
	message := item.Question
	if message == "" {
		message = "生产返回 HTTP 502；同时请根据 fixture.md 核对配置。"
	}
	plan, err := planner.Plan(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	evidenceID, rootID := item.Expected.EvidenceIDs[0], item.Expected.RootCauseIDs[0]
	evidence := []orchestration.SharedEvidence{
		{ID: evidenceID, SourceType: "document_chunk", Summary: "fixture", SourceID: "doc", Score: 1},
		{ID: "user-observation:test", SourceType: "user_observation", Summary: "fixture", SourceID: "observation", Score: 1},
	}
	claims := []orchestration.SynthesizedClaim{
		{ID: "knowledge-grounded-answer", Kind: "project_knowledge", Statement: "fixture fact", Confidence: 1, Status: orchestration.ClaimSupported, SourceAgents: []string{orchestration.KnowledgeAgentRole}, EvidenceRefs: []string{evidenceID}, CitationIDs: []string{"C1"}},
		{ID: rootID, Kind: "diagnostic_hypothesis", Statement: "fixture cause", Confidence: 1, Status: orchestration.ClaimSupported, SourceAgents: []string{orchestration.DiagnosticAgentRole}, EvidenceRefs: []string{"user-observation:test"}, CitationIDs: []string{"C2"}},
	}
	return orchestration.CollaborationRun{
		SchemaVersion: orchestration.CollaborationRunSchemaVersion, Mode: "shadow_only", AffectsLiveTraffic: false,
		Executed: true, Status: orchestration.SynthesisComplete, ReasonCode: "all_claims_citation_verified", Plan: plan,
		Execution: &orchestration.ExecutionResult{
			SchemaVersion: orchestration.ExecutionSchemaVersion, ExecutorVersion: orchestration.ExecutorVersion, Mode: "shadow_only",
			Status: orchestration.ExecutionCompleted, TaskResults: []orchestration.TaskExecution{{Agent: orchestration.KnowledgeAgentRole}, {Agent: orchestration.DiagnosticAgentRole}},
			Usage: orchestration.BudgetUsage{Agents: 2, ToolCalls: 2, Iterations: 2, InputTokens: 100, OutputTokens: 50, CostMicros: 10},
		},
		Synthesis: &orchestration.SynthesisResult{
			SchemaVersion: orchestration.SynthesisSchemaVersion, SynthesizerVersion: orchestration.SynthesizerVersion, Mode: "shadow_only",
			Status: orchestration.SynthesisComplete, Claims: claims, Evidence: evidence,
			Citations: []orchestration.SynthesizedCitation{{CitationID: "C1", EvidenceID: evidenceID}, {CitationID: "C2", EvidenceID: "user-observation:test"}},
			Conflicts: []orchestration.ClaimConflict{}, RejectedClaims: []orchestration.RejectedClaim{}, DegradedAgents: []string{},
		},
	}
}
