package evaluation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	knowledgeagent "GopherAI/internal/agent/knowledge"
	"GopherAI/internal/contract"
	"GopherAI/internal/rag"
)

type parentAnswererStub struct {
	outputs map[string]knowledgeagent.Output
}

func (stub parentAnswererStub) Answer(_ context.Context, input knowledgeagent.Input) (knowledgeagent.Output, error) {
	output, exists := stub.outputs[input.Question]
	if !exists {
		return knowledgeagent.Output{}, fmt.Errorf("missing output for %s", input.Question)
	}
	return output, nil
}

func TestLoadParentContextCasesRequiresBalancedVersionedDataset(t *testing.T) {
	var encoded bytes.Buffer
	for index := 0; index < ParentContextCaseCount; index++ {
		slice := ParentContextSliceTarget
		if index >= ParentContextTargetCaseCount {
			slice = ParentContextSliceGuard
		}
		item := ParentContextCase{
			ID: fmt.Sprintf("parent-%02d", index+1), Slice: slice, Question: fmt.Sprintf("question-%02d", index+1),
			Expected:   ParentContextExpected{EvidenceIDs: []string{"child-1"}, AnswerFacts: []string{"47"}, ShouldResolve: true},
			ReviewedBy: "pending_user", DatasetVersion: ParentContextDatasetVersion,
		}
		line, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		encoded.Write(line)
		encoded.WriteByte('\n')
	}
	cases, err := LoadParentContextCases(&encoded)
	if err != nil || len(cases) != ParentContextCaseCount {
		t.Fatalf("load balanced dataset: cases=%d err=%v", len(cases), err)
	}
}

func TestEvaluateParentContextABSeparatesTechnicalAndPromotionGates(t *testing.T) {
	cases := make([]ParentContextCase, 0, ParentContextCaseCount)
	baselineOutputs := make(map[string]knowledgeagent.Output)
	candidateOutputs := make(map[string]knowledgeagent.Output)
	for index := 0; index < ParentContextCaseCount; index++ {
		slice := ParentContextSliceTarget
		if index >= ParentContextTargetCaseCount {
			slice = ParentContextSliceGuard
		}
		question := fmt.Sprintf("question-%02d", index+1)
		cases = append(cases, ParentContextCase{
			ID: fmt.Sprintf("case-%02d", index+1), Slice: slice, Question: question,
			Expected:   ParentContextExpected{EvidenceIDs: []string{"child-a"}, AnswerFacts: []string{"timeout=47"}, ShouldResolve: true},
			ReviewedBy: "pending_user", DatasetVersion: ParentContextDatasetVersion,
		})
		baselineAnswer := "timeout unknown"
		if slice == ParentContextSliceGuard {
			baselineAnswer = "timeout=47 [E1]"
		}
		baselineOutputs[question] = parentEvaluationOutput(baselineAnswer, false, 100, []contract.Evidence{
			parentEvidence("child-a", "doc-a", "parent-a"),
		})
		candidateOutputs[question] = parentEvaluationOutput("timeout=47 [E1]", true, 120, []contract.Evidence{
			parentEvidence("child-a", "doc-a", "parent-a"), parentEvidence("child-b", "doc-b", "parent-b"),
		})
	}
	report, err := EvaluateParentContextAB(context.Background(), cases, "tenant", "user", "candidate-sha", time.Unix(1, 0), parentAnswererStub{baselineOutputs}, parentAnswererStub{candidateOutputs})
	if err != nil {
		t.Fatal(err)
	}
	if !report.TechnicalGatesPassed || !report.NetBenefitPassed {
		t.Fatalf("expected measurable technical/net benefit, got failures=%v metrics=%+v", report.GateFailures, report.Metrics)
	}
	if report.HumanReviewed || report.PromotionEligible || report.RecommendedDefaultWeight != 0 {
		t.Fatalf("pending labels must keep promotion disabled: %+v", report)
	}
	if report.Metrics.TargetQualityDeltaCI95Lower <= 0 || report.Metrics.MeanDocumentDiversityDelta != 1 || report.Metrics.ChildCitationIntegrityRate != 1 {
		t.Fatalf("unexpected paired metrics: %+v", report.Metrics)
	}
}

func TestParentContextSafetyRejectsParentCitation(t *testing.T) {
	output := parentEvaluationOutput("bad [E1]", true, 100, []contract.Evidence{parentEvidence("child-a", "doc-a", "parent-a")})
	output.Result.Citations[0].EvidenceID = "parent-a"
	passed, reasons := parentContextSafety(output, "tenant")
	if passed || !contains(reasons, "parent_used_as_citation") {
		t.Fatalf("parent citation must fail closed: passed=%t reasons=%v", passed, reasons)
	}
}

func parentEvaluationOutput(answer string, parent bool, inputTokens int, evidence []contract.Evidence) knowledgeagent.Output {
	diagnostics := rag.SearchDiagnostics{}
	if parent {
		diagnostics.Parent = &rag.ParentContextDiagnostics{Version: rag.ParentContextStrategyVersion, ParentContextHits: len(evidence), ChildCitationOnly: true}
	}
	return knowledgeagent.Output{
		Result: contract.AgentResult{
			Answer: answer, Evidence: evidence, Citations: []contract.Citation{{ID: "citation-1", EvidenceID: "child-a"}},
			Resolved: true, Usage: contract.ModelUsage{InputTokens: inputTokens, OutputTokens: 20},
		},
		Diagnostics: diagnostics,
	}
}

func parentEvidence(id string, documentID string, parentID string) contract.Evidence {
	return contract.Evidence{ID: id, TenantID: "tenant", SourceID: documentID, ParentEvidenceID: parentID, ParentContext: "parent context"}
}
