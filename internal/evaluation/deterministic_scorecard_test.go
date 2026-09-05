package evaluation

import (
	"testing"
	"time"
)

func TestBuildDeterministicScorecardKeepsReviewGateSeparate(t *testing.T) {
	intentReport := IntentCascadeReport{
		DatasetVersion: IntentDatasetVersion, CaseCount: 1, MacroF1: .95, MinimumRecall: 1,
		TechnicalGatePassed: true, HumanReviewed: false,
		LabelMetrics: map[string]IntentLabelMetrics{"project_qa": {Support: 1, Correct: 1, Recall: 1}},
	}
	ragReport := RAGReport{
		DatasetVersion: RAGCoreDatasetVersion, CaseCount: 2, PositiveCaseCount: 1, NoEvidenceCaseCount: 1, Passed: true,
		Metrics: RAGMetrics{NDCGAt5: 1},
		Cases: []RAGCaseResult{
			{ID: "rag", ExpectedToResolve: true, RecallAt5: 1, CitationCovered: true},
			{ID: "rag-safe", ExpectedToResolve: false, SafeRejection: true},
		},
	}
	diagnosticReport := DiagnosticEvaluationReport{
		DatasetVersion: DiagnosticDatasetVersion, TechnicalGatesPassed: true,
		Metrics: DiagnosticEvaluationMetrics{CaseCount: 1},
		Cases:   []DiagnosticEvaluationCaseResult{{ID: "diagnosis", RootCauseHit: true, StepCoverage: 1, VerificationCorrect: true, ReadOnly: true}},
	}
	toolReport := ToolEvaluationReport{
		DatasetVersion: ToolDatasetVersion, TechnicalGatesPassed: true,
		Metrics: ToolEvaluationMetrics{CaseCount: 1, SchemaContractPassRate: 1, ResiliencePassRate: 1},
		Cases:   []ToolEvaluationCaseResult{{ID: "tool", Category: "selection", Passed: true}},
	}
	memoryReport := MemoryEvaluationReport{
		DatasetVersion: MemoryDatasetVersion, TechnicalGatesPassed: true,
		Metrics: MemoryEvaluationMetrics{CaseCount: 1, ContextBudgetPassRate: 1},
		Cases:   []MemoryEvaluationCaseResult{{ID: "memory", ExpectedKeys: []string{"redis_version"}, ActualKeys: []string{"redis_version"}, WithinBudget: true}},
	}
	card, err := BuildDeterministicScorecard("candidate", time.Unix(1, 0), intentReport, ragReport, diagnosticReport, toolReport, memoryReport)
	if err != nil {
		t.Fatal(err)
	}
	if !card.TechnicalGatesPassed || card.CaseCount != 6 || card.CompletedCases != 6 {
		t.Fatalf("unexpected technical scorecard: %+v", card)
	}
	if card.HumanReviewed || card.BaselineEligible {
		t.Fatalf("pending source labels must block baseline: %+v", card)
	}
	for _, slice := range card.Slices {
		for _, metric := range slice.Metrics {
			if metric.Denominator <= 0 || metric.Direction == "" || metric.IntervalMethod == "" {
				t.Fatalf("metric lacks reproducible accounting: %+v", metric)
			}
		}
	}
}

func TestWilsonIntervalReportsSmallSampleUncertainty(t *testing.T) {
	lower, upper := wilsonInterval(10, 10, 1.959963984540054)
	if lower >= 1 || upper < .999 || lower < .7 {
		t.Fatalf("unexpected Wilson interval for 10/10: [%f, %f]", lower, upper)
	}
}

func TestMetricDirectionHandlesLowerAndZeroTargets(t *testing.T) {
	if !metricMeetsTarget(.03, MetricLowerIsBetter, .05) || metricMeetsTarget(.06, MetricLowerIsBetter, .05) {
		t.Fatal("lower-is-better direction is wrong")
	}
	if !metricMeetsTarget(0, MetricMustBeZero, 0) || metricMeetsTarget(1, MetricMustBeZero, 0) {
		t.Fatal("must-be-zero direction is wrong")
	}
}
