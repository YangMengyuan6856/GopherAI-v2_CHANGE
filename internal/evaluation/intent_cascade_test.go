package evaluation

import (
	"GopherAI/internal/contract"
	"GopherAI/internal/intent"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

type scriptedCascade struct {
	decisions map[string]intent.CascadeDecision
}

func (cascade scriptedCascade) Recognize(_ context.Context, input intent.CascadeInput) intent.CascadeDecision {
	return cascade.decisions[input.Question]
}

func TestEvaluateCascadeReportsG4AndCalibrationWithoutClaimingPendingBaseline(t *testing.T) {
	cases := make([]IntentCase, 0, 6)
	decisions := make(map[string]intent.CascadeDecision, 6)
	for index, label := range intent.Labels() {
		question := "question-" + label
		cases = append(cases, IntentCase{ID: question, Question: question, Expected: IntentExpected{Intent: label}, ReviewedBy: "pending_user"})
		predicted := label
		if label == intent.Troubleshooting {
			predicted = intent.General
		}
		decisions[question] = intent.CascadeDecision{
			Result:      contract.IntentResult{Intent: predicted, Confidence: .9, Version: intent.CascadeVersion},
			Diagnostics: intent.CascadeDiagnostics{FinalStage: "llm", PrototypeCalled: true, LLMCalled: index%2 == 0},
		}
	}
	report := EvaluateCascade(context.Background(), cases, scriptedCascade{decisions: decisions}, "candidate", time.Unix(1, 0))
	if report.Accuracy != float64(5)/6 || report.SevereMisrouteRate != float64(1)/6 {
		t.Fatalf("unexpected headline metrics: %+v", report)
	}
	if report.LLMCallRate != .5 || report.PrototypeCallRate != 1 {
		t.Fatalf("unexpected stage call rates: %+v", report)
	}
	if report.HumanReviewed || report.BaselineEligible || report.TechnicalGatePassed {
		t.Fatalf("pending labels or failed gates must not become a baseline: %+v", report)
	}
	if report.LabelMetrics[intent.Troubleshooting].Recall != 0 || report.Confusion[intent.Troubleshooting][intent.General] != 1 {
		t.Fatalf("per-label metrics or confusion missing: %+v", report)
	}
	var markdown bytes.Buffer
	if err := WriteIntentCascadeMarkdown(&markdown, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markdown.String(), "not a resume-grade baseline") {
		t.Fatalf("human review warning missing: %s", markdown.String())
	}
}

func TestEvaluateCascadePassesTechnicalGateWithReviewedPerfectSet(t *testing.T) {
	cases := make([]IntentCase, 0, 6)
	decisions := make(map[string]intent.CascadeDecision, 6)
	for _, label := range intent.Labels() {
		cases = append(cases, IntentCase{ID: label, Question: label, Expected: IntentExpected{Intent: label}, ReviewedBy: "human"})
		decisions[label] = intent.CascadeDecision{Result: contract.IntentResult{Intent: label, Confidence: 1}, Diagnostics: intent.CascadeDiagnostics{FinalStage: "pattern"}}
	}
	report := EvaluateCascade(context.Background(), cases, scriptedCascade{decisions: decisions}, "candidate", time.Now())
	if !report.TechnicalGatePassed || !report.BaselineEligible || report.MacroF1 != 1 || report.MinimumRecall != 1 {
		t.Fatalf("perfect reviewed set should pass: %+v", report)
	}
}
