package evaluation

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestContextDatasetAndEvaluator(t *testing.T) {
	dataset, err := os.Open("../../evals/devsupport-context-compression-v1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer dataset.Close()
	cases, summary, err := LoadContextDataset(dataset)
	if err != nil {
		t.Fatal(err)
	}
	if summary.CaseCount != 12 || summary.HumanReviewed {
		t.Fatalf("unexpected dataset summary: %+v", summary)
	}
	for _, outcome := range contextOutcomes {
		if summary.OutcomeCounts[outcome] != 3 {
			t.Fatalf("outcome %s is not balanced: %+v", outcome, summary.OutcomeCounts)
		}
	}
	report := EvaluateContextCompression(cases, summary, time.Date(2026, 9, 5, 5, 0, 0, 0, time.UTC))
	if !report.TechnicalGatesPassed || report.BaselineEligible || report.Metrics.OverBudgetCases != 0 || report.Metrics.DeterministicReplayRate != 1 {
		t.Fatalf("unexpected report gates: %+v", report)
	}
	if report.Metrics.ConstraintRetention < 0.95 || report.Metrics.ConfirmedFactRetention < 0.95 || report.Metrics.OpenQuestionRetention < 0.90 || report.Metrics.AverageTokenReduction <= 0 {
		t.Fatalf("unexpected context metrics: %+v", report.Metrics)
	}
}

func TestContextDatasetRejectsUnknownFields(t *testing.T) {
	_, _, err := LoadContextDataset(strings.NewReader(`{"id":"bad","unknown":true}`))
	if err == nil {
		t.Fatal("unknown field was accepted")
	}
}
