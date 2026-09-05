package orchestration

import (
	"context"
	"errors"
	"testing"

	knowledgeagent "GopherAI/internal/agent/knowledge"
	"GopherAI/internal/contract"
	"GopherAI/internal/diagnostic"
)

type knowledgeAnswererStub struct {
	output knowledgeagent.Output
	err    error
}

func (stub knowledgeAnswererStub) Answer(context.Context, knowledgeagent.Input) (knowledgeagent.Output, error) {
	return stub.output, stub.err
}

type caseAnalyzerStub struct {
	output diagnostic.CaseStrategyResult
	err    error
}

func (stub caseAnalyzerStub) Analyze(context.Context, string, string, string) (diagnostic.CaseStrategyResult, error) {
	return stub.output, stub.err
}

func TestKnowledgeRunnerOnlyClaimsCitedGroundedAnswer(t *testing.T) {
	answerer := knowledgeAnswererStub{output: knowledgeagent.Output{
		Result: contract.AgentResult{
			Answer: "发布探针期望 204。", Confidence: .91,
			Evidence: []contract.Evidence{{
				ID: "chunk-1", Kind: "document_chunk", TenantID: "alice", SourceID: "doc-1", SourceVersion: "2",
				Title: "发布手册", LineStart: 4, LineEnd: 8, Content: "probe_code=204", ContentHash: "hash-1", Score: .91,
			}},
			Citations: []contract.Citation{{ID: "C1", EvidenceID: "chunk-1"}},
			Usage:     contract.ModelUsage{InputTokens: 100, OutputTokens: 20},
		},
		Answer: knowledgeagent.AnswerDiagnostics{ReasonCode: knowledgeagent.AnswerReasonCompleted, ModelAttempts: 1},
	}}
	runner, _ := NewKnowledgeRunner(answerer)
	output, err := runner.Run(context.Background(), PlannedTask{Agent: KnowledgeAgentRole}, ExecutionInput{TenantID: "alice", UserID: "alice", Message: "核对发布手册"})
	if err != nil || len(output.Claims) != 1 || len(output.Evidence) != 1 || output.Claims[0].EvidenceRefs[0] != "chunk-1" || output.ToolCalls != 1 {
		t.Fatalf("grounded knowledge mapping failed: output=%+v err=%v", output, err)
	}
}

func TestKnowledgeRunnerDoesNotPromoteGateFallbackToClaim(t *testing.T) {
	runner, _ := NewKnowledgeRunner(knowledgeAnswererStub{output: knowledgeagent.Output{
		Result: contract.AgentResult{Answer: "证据不足", NeedsUserInput: true},
		Answer: knowledgeagent.AnswerDiagnostics{ReasonCode: "no_evidence"},
	}})
	output, err := runner.Run(context.Background(), PlannedTask{Agent: KnowledgeAgentRole}, ExecutionInput{TenantID: "alice", UserID: "alice", Message: "未知配置"})
	if err != nil || len(output.Claims) != 0 || output.OutputReason != "no_evidence" {
		t.Fatalf("gate fallback was promoted to a claim: output=%+v err=%v", output, err)
	}
}

func TestDiagnosticRunnerKeepsHistoricalCaseAdvisory(t *testing.T) {
	analyzer := caseAnalyzerStub{output: diagnostic.CaseStrategyResult{
		Baseline: diagnostic.Result{
			Hypotheses: []diagnostic.Hypothesis{{
				ID: "redis-auth", Cause: "Redis 认证不一致", Rationale: "命中 NOAUTH", Confidence: .93,
				Evidence: []diagnostic.EvidenceReference{{ID: "user-observation:noauth", SourceType: diagnostic.EvidenceUserObservation, Summary: "命中 NOAUTH"}},
			}},
		},
		CaseMemoryStatus: diagnostic.CaseMemoryHit, CaseStrength: diagnostic.CaseStrengthStrong,
		ReasonCode:             "strong_case_prioritization_candidate",
		PriorityRecommendation: &diagnostic.CasePriorityRecommendation{HypothesisID: "redis-auth", Similarity: .95, AdvisoryOnly: true},
	}}
	runner, _ := NewDiagnosticRunner(analyzer)
	output, err := runner.Run(context.Background(), PlannedTask{Agent: DiagnosticAgentRole}, ExecutionInput{TenantID: "alice", UserID: "alice", Message: "Redis NOAUTH"})
	if err != nil || len(output.Claims) != 1 || len(output.Evidence) != 1 || output.Claims[0].EvidenceRefs[0] != "user-observation:noauth" {
		t.Fatalf("diagnostic mapping failed: output=%+v err=%v", output, err)
	}
	for _, item := range output.Evidence {
		if item.SourceType == diagnostic.EvidenceResolvedCase {
			t.Fatalf("historical case was promoted to current evidence: %+v", item)
		}
	}
}

func TestRunnerPropagatesDependencyFailure(t *testing.T) {
	want := errors.New("dependency unavailable")
	runner, _ := NewKnowledgeRunner(knowledgeAnswererStub{err: want})
	_, err := runner.Run(context.Background(), PlannedTask{Agent: KnowledgeAgentRole}, ExecutionInput{TenantID: "alice", UserID: "alice", Message: "核对"})
	if !errors.Is(err, want) {
		t.Fatalf("dependency error was hidden: %v", err)
	}
}
