package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"GopherAI/internal/evaluation"
)

func main() {
	datasetPath := flag.String("dataset", "evals/devsupport-tool-runtime-v1.jsonl", "tool runtime JSONL dataset")
	reportPath := flag.String("report", "evals/results/devsupport-tool-runtime-v1-candidate.json", "output report")
	flag.Parse()
	dataset, err := os.Open(*datasetPath)
	if err != nil {
		fatalToolEval(err)
	}
	defer dataset.Close()
	cases, summary, err := evaluation.LoadToolDataset(dataset)
	if err != nil {
		fatalToolEval(err)
	}
	report := evaluation.EvaluateToolRuntime(cases, summary, time.Now())
	encoded, err := evaluation.MarshalToolEvaluationReport(report)
	if err != nil {
		fatalToolEval(err)
	}
	if err := os.MkdirAll(filepath.Dir(*reportPath), 0o755); err != nil {
		fatalToolEval(err)
	}
	if err := os.WriteFile(*reportPath, append(encoded, '\n'), 0o644); err != nil {
		fatalToolEval(err)
	}
	fmt.Printf("tool eval: cases=%d selection=%.4f schema=%.4f authorization=%.4f resilience=%.4f safety=%.4f dangerous=%.4f unknown=%d audit=%.4f deterministic=%.4f repair=%.4f no_progress=%.4f technical_gates=%v baseline_eligible=%v\n",
		report.Metrics.CaseCount, report.Metrics.ToolSelectionAccuracy, report.Metrics.SchemaContractPassRate,
		report.Metrics.AuthorizationPolicyPassRate, report.Metrics.ResiliencePassRate, report.Metrics.SafetyPassRate,
		report.Metrics.DangerousActionExecutionRate, report.Metrics.UnknownToolExecutionCount,
		report.Metrics.AuditCoverageRate, report.Metrics.DeterministicReplayRate, report.Metrics.BoundedRepairPassRate, report.Metrics.NoProgressTerminationRate,
		report.TechnicalGatesPassed, report.BaselineEligible)
}

func fatalToolEval(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
