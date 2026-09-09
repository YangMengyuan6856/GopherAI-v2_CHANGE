package evaluation

import (
	"os"
	"strings"
	"testing"
	"time"

	memorydomain "GopherAI/internal/memory"
	"GopherAI/internal/profilememory"
)

func loadMemoryFixture(t *testing.T) ([]MemoryCase, MemoryDatasetSummary) {
	t.Helper()
	file, err := os.Open("../../evals/devsupport-memory-v1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	cases, summary, err := LoadMemoryDataset(file)
	if err != nil {
		t.Fatal(err)
	}
	return cases, summary
}

func TestMemoryDatasetHasTwentyBalancedPendingCases(t *testing.T) {
	cases, summary := loadMemoryFixture(t)
	if len(cases) != 20 || summary.CaseCount != 20 || summary.HumanReviewed {
		t.Fatalf("unexpected memory dataset summary: %+v", summary)
	}
	for _, category := range memoryCategories {
		if summary.CategoryCounts[category] != 4 {
			t.Fatalf("category %s is unbalanced: %+v", category, summary.CategoryCounts)
		}
	}
}

func TestMemoryDatasetRejectsUnknownFieldsAndWrongCardinality(t *testing.T) {
	invalid := `{"id":"one","category":"relevant","query":"Redis","limit":5,"budget_tokens":256,"facts":[],"expected":{"included_keys":[],"forbidden_values":[]},"reviewed_by":"pending_user","dataset_version":"devsupport-memory-v1","unexpected":true}`
	if _, _, err := LoadMemoryDataset(strings.NewReader(invalid)); err == nil {
		t.Fatal("invalid memory dataset was accepted")
	}
}

func TestMemoryEvaluationPassesSafetyGatesButIsNotBaselineEligible(t *testing.T) {
	cases, summary := loadMemoryFixture(t)
	report := EvaluateMemory(profilememory.NewSelector(), memorydomain.NewAssembler(), cases, summary, time.Date(2026, 9, 5, 4, 30, 0, 0, time.UTC))
	if !report.TechnicalGatesPassed || report.BaselineEligible || report.HumanReviewed {
		t.Fatalf("unexpected memory evaluation gate: %+v", report)
	}
	if report.Metrics.RelevantMemoryRecall < 0.90 || report.Metrics.StaleWrongInjectionRate != 0 || report.Metrics.DeletedMemoryRecall != 0 || report.Metrics.CrossPrincipalLeakage != 0 || report.Metrics.ContextBudgetPassRate != 1 || report.Metrics.DeterministicReplayRate != 1 {
		t.Fatalf("memory safety metrics failed: %+v", report.Metrics)
	}
	if len(report.Cases) != 20 {
		t.Fatalf("case details missing: %d", len(report.Cases))
	}
}
