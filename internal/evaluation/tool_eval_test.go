package evaluation

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestToolDatasetAndEvaluator(t *testing.T) {
	dataset, err := os.Open("../../evals/devsupport-tool-runtime-v1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer dataset.Close()
	cases, summary, err := LoadToolDataset(dataset)
	if err != nil {
		t.Fatal(err)
	}
	if summary.CaseCount != 30 || summary.HumanReviewed {
		t.Fatalf("unexpected dataset summary: %+v", summary)
	}
	for _, category := range toolEvaluationCategories {
		if summary.CategoryCounts[category] != 6 {
			t.Fatalf("category %s is not balanced: %+v", category, summary.CategoryCounts)
		}
	}
	report := EvaluateToolRuntime(cases, summary, time.Date(2026, 9, 5, 6, 0, 0, 0, time.UTC))
	if !report.TechnicalGatesPassed || report.BaselineEligible || report.Metrics.CaseCount != 30 {
		t.Fatalf("unexpected report gates: %+v", report)
	}
	if report.Metrics.ToolSelectionAccuracy != 1 || report.Metrics.SchemaContractPassRate != 1 || report.Metrics.AuthorizationPolicyPassRate != 1 || report.Metrics.ResiliencePassRate != 1 || report.Metrics.SafetyPassRate != 1 {
		t.Fatalf("category contract regression: %+v", report.Metrics)
	}
	if report.Metrics.DangerousActionExecutionRate != 0 || report.Metrics.UnknownToolExecutionCount != 0 || report.Metrics.AuditCoverageRate != 1 || report.Metrics.DeterministicReplayRate != 1 {
		t.Fatalf("safety or replay regression: %+v", report.Metrics)
	}
}

func TestToolDatasetRejectsUnknownFields(t *testing.T) {
	_, _, err := LoadToolDataset(strings.NewReader(`{"id":"bad","unknown":true}`))
	if err == nil {
		t.Fatal("unknown field was accepted")
	}
}

func TestToolDatasetRequiresExactBalancedCaseCount(t *testing.T) {
	_, _, err := LoadToolDataset(strings.NewReader(`{"id":"one","category":"selection","scenario":"manifest_selection","message":"当前发布清单","expected":{"decision":"execute","tool_names":["deployment_manifest_lookup"],"cached":false,"executions":0,"audit_count":0},"reviewed_by":"pending_user","dataset_version":"devsupport-tool-runtime-v1"}`))
	if err == nil || !strings.Contains(err.Error(), "must contain 30") {
		t.Fatalf("expected exact case-count rejection, got %v", err)
	}
}
