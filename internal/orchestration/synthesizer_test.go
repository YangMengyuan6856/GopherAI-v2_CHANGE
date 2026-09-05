package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func synthesisExecution(tasks ...TaskExecution) ExecutionResult {
	return ExecutionResult{
		SchemaVersion: ExecutionSchemaVersion, ExecutorVersion: ExecutorVersion,
		Mode: "shadow_only", AffectsLiveTraffic: false, Status: ExecutionCompleted, TaskResults: tasks,
	}
}

func successfulTask(index int, agent string, output AgentOutput) TaskExecution {
	return TaskExecution{Index: index, TaskID: strings.ToLower(agent), Agent: agent, Status: TaskStatusSucceeded, ReasonCode: "task_completed", Output: output}
}

func evidenceFor(tenantID string, id string, summary string, score float64) SharedEvidence {
	return SharedEvidence{ID: id, SourceType: "project_document", Summary: summary, TenantID: tenantID, SourceID: "document-1", SourceVersion: "v1", LineStart: 1, LineEnd: 2, ContentHash: "sha-1", Score: score}
}

func TestEvidenceAwareSynthesizerDeduplicatesClaimsAndEvidence(t *testing.T) {
	evidenceLow := evidenceFor("alice", "E1", "后端监听 9090", .8)
	evidenceHigh := evidenceLow
	evidenceHigh.Score = .95
	claimOne := AgentClaim{ID: "knowledge-port", Kind: "project_fact", Statement: "后端监听 9090。", EvidenceRefs: []string{"E1"}, Confidence: .8}
	claimTwo := AgentClaim{ID: "diagnostic-port", Kind: "project_fact", Statement: "后端监听 9090。", EvidenceRefs: []string{"E1"}, Confidence: .9}
	execution := synthesisExecution(
		successfulTask(2, DiagnosticAgentRole, AgentOutput{Claims: []AgentClaim{claimTwo}, Evidence: []SharedEvidence{evidenceHigh}}),
		successfulTask(1, KnowledgeAgentRole, AgentOutput{Claims: []AgentClaim{claimOne}, Evidence: []SharedEvidence{evidenceLow}}),
	)
	result, err := NewEvidenceAwareSynthesizer().Synthesize(context.Background(), execution, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != SynthesisComplete || len(result.Claims) != 1 || len(result.Evidence) != 1 || len(result.Citations) != 1 || result.Evidence[0].Score != .95 {
		t.Fatalf("claim/evidence deduplication failed: %+v", result)
	}
	if len(result.Claims[0].SourceAgents) != 2 || result.Claims[0].CitationIDs[0] != "C1" || !strings.Contains(result.UnifiedAnswer, "[C1]") {
		t.Fatalf("deduplicated claim lost lineage: %+v", result.Claims[0])
	}
}

func TestEvidenceAwareSynthesizerRejectsUnknownCitation(t *testing.T) {
	output := AgentOutput{Claims: []AgentClaim{{ID: "claim-1", Kind: "project_fact", Statement: "未经支持的事实", EvidenceRefs: []string{"UNKNOWN"}, Confidence: .9}}, Evidence: []SharedEvidence{}}
	result, err := NewEvidenceAwareSynthesizer().Synthesize(context.Background(), synthesisExecution(successfulTask(1, KnowledgeAgentRole, output)), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != SynthesisInsufficient || result.FallbackStrategy != "diagnosis_standard" || len(result.Claims) != 0 || len(result.RejectedClaims) != 1 || result.RejectedClaims[0].ReasonCode != "unknown_evidence_reference" {
		t.Fatalf("unknown citation was accepted: %+v", result)
	}
}

func TestEvidenceAwareSynthesizerRejectsCollidingEvidenceID(t *testing.T) {
	left := evidenceFor("alice", "E1", "端口 9090", .9)
	right := evidenceFor("alice", "E1", "端口 8080", .9)
	leftClaim := AgentClaim{ID: "left", Kind: "project_fact", Statement: "端口 9090", EvidenceRefs: []string{"E1"}, Confidence: .9}
	rightClaim := AgentClaim{ID: "right", Kind: "project_fact", Statement: "端口 8080", EvidenceRefs: []string{"E1"}, Confidence: .9}
	execution := synthesisExecution(
		successfulTask(1, KnowledgeAgentRole, AgentOutput{Claims: []AgentClaim{leftClaim}, Evidence: []SharedEvidence{left}}),
		successfulTask(2, DiagnosticAgentRole, AgentOutput{Claims: []AgentClaim{rightClaim}, Evidence: []SharedEvidence{right}}),
	)
	result, err := NewEvidenceAwareSynthesizer().Synthesize(context.Background(), execution, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Claims) != 0 || len(result.Evidence) != 0 || len(result.RejectedClaims) != 2 {
		t.Fatalf("colliding evidence ID escaped synthesis: %+v", result)
	}
	for _, rejected := range result.RejectedClaims {
		if rejected.ReasonCode != "evidence_id_collision" {
			t.Fatalf("unexpected collision reason: %+v", rejected)
		}
	}
}

func TestEvidenceAwareSynthesizerMarksExclusiveValueConflictWithoutChoosingWinner(t *testing.T) {
	evidenceOne := evidenceFor("alice", "E1", "手册写 9090", .9)
	evidenceTwo := evidenceFor("alice", "E2", "清单写 8080", .9)
	evidenceTwo.SourceID, evidenceTwo.ContentHash = "document-2", "sha-2"
	claimOne := AgentClaim{ID: "port-manual", Kind: "project_fact", Statement: "部署手册声明端口 9090。", EvidenceRefs: []string{"E1"}, Confidence: .9, ConflictKey: "backend.port", ConflictValue: "9090"}
	claimTwo := AgentClaim{ID: "port-manifest", Kind: "project_fact", Statement: "发布清单声明端口 8080。", EvidenceRefs: []string{"E2"}, Confidence: .9, ConflictKey: "backend.port", ConflictValue: "8080"}
	execution := synthesisExecution(successfulTask(1, KnowledgeAgentRole, AgentOutput{Claims: []AgentClaim{claimOne, claimTwo}, Evidence: []SharedEvidence{evidenceOne, evidenceTwo}}))
	result, err := NewEvidenceAwareSynthesizer().Synthesize(context.Background(), execution, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != SynthesisConflict || result.FallbackStrategy != "diagnosis_standard" || len(result.Conflicts) != 1 || len(result.Conflicts[0].Values) != 2 {
		t.Fatalf("exclusive conflict was silently resolved: %+v", result)
	}
	for _, claim := range result.Claims {
		if claim.Status != ClaimConflicted {
			t.Fatalf("conflicting claim was presented as supported winner: %+v", claim)
		}
	}
	if !strings.Contains(result.UnifiedAnswer, "不会静默选边") {
		t.Fatalf("unified answer hid conflict: %s", result.UnifiedAnswer)
	}
}

func TestEvidenceAwareSynthesizerKeepsVerifiedPartialResultWhenSiblingFails(t *testing.T) {
	evidence := evidenceFor("alice", "E1", "Redis NOAUTH", .9)
	claim := AgentClaim{ID: "redis", Kind: "diagnostic_hypothesis", Statement: "Redis 认证配置需要核对。", EvidenceRefs: []string{"E1"}, Confidence: .8}
	execution := synthesisExecution(
		TaskExecution{Index: 1, TaskID: "knowledge", Agent: KnowledgeAgentRole, Status: TaskStatusTimedOut, ReasonCode: "task_timeout_exceeded", Output: emptyAgentOutput()},
		successfulTask(2, DiagnosticAgentRole, AgentOutput{Claims: []AgentClaim{claim}, Evidence: []SharedEvidence{evidence}}),
	)
	result, err := NewEvidenceAwareSynthesizer().Synthesize(context.Background(), execution, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != SynthesisPartial || result.FallbackStrategy != "diagnosis_standard" || len(result.Claims) != 1 || len(result.DegradedAgents) != 1 || result.DegradedAgents[0] != KnowledgeAgentRole {
		t.Fatalf("verified sibling result was lost during partial fallback: %+v", result)
	}
}

func TestEvidenceAwareSynthesizerRejectsForgedSucceededTaskPayload(t *testing.T) {
	output := AgentOutput{
		Claims:   []AgentClaim{{ID: "claim-1", Kind: "project_fact", Statement: "端口 9090", EvidenceRefs: []string{"E1"}, Confidence: .9}},
		Evidence: []SharedEvidence{{ID: "E1", SourceType: "project_document", Summary: "端口 9090", TenantID: "mallory", Score: .9}},
	}
	result, err := NewEvidenceAwareSynthesizer().Synthesize(context.Background(), synthesisExecution(successfulTask(1, KnowledgeAgentRole, output)), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != SynthesisInsufficient || len(result.Claims) != 0 || len(result.DegradedAgents) != 1 || result.DegradedAgents[0] != KnowledgeAgentRole {
		t.Fatalf("forged succeeded task bypassed synthesis validation: %+v", result)
	}
}

func TestEvidenceAwareSynthesizerPropagatesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewEvidenceAwareSynthesizer().Synthesize(ctx, synthesisExecution(), "alice")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation was masked: %v", err)
	}
}
