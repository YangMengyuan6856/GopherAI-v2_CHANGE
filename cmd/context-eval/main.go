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
	datasetPath := flag.String("dataset", "evals/devsupport-context-compression-v1.jsonl", "context compression JSONL dataset")
	reportPath := flag.String("report", "evals/results/devsupport-context-compression-v1-candidate.json", "output report")
	flag.Parse()
	dataset, err := os.Open(*datasetPath)
	if err != nil {
		fatalContextEval(err)
	}
	defer dataset.Close()
	cases, summary, err := evaluation.LoadContextDataset(dataset)
	if err != nil {
		fatalContextEval(err)
	}
	report := evaluation.EvaluateContextCompression(cases, summary, time.Now())
	encoded, err := evaluation.MarshalContextReport(report)
	if err != nil {
		fatalContextEval(err)
	}
	if err := os.MkdirAll(filepath.Dir(*reportPath), 0o755); err != nil {
		fatalContextEval(err)
	}
	if err := os.WriteFile(*reportPath, append(encoded, '\n'), 0o644); err != nil {
		fatalContextEval(err)
	}
	fmt.Printf("context eval: cases=%d constraints=%.4f facts=%.4f open=%.4f next=%.4f reduction=%.4f over_budget=%d deterministic=%.4f technical_gates=%v baseline_eligible=%v\n",
		report.Metrics.CaseCount, report.Metrics.ConstraintRetention, report.Metrics.ConfirmedFactRetention,
		report.Metrics.OpenQuestionRetention, report.Metrics.NextActionRetention, report.Metrics.AverageTokenReduction,
		report.Metrics.OverBudgetCases, report.Metrics.DeterministicReplayRate, report.TechnicalGatesPassed, report.BaselineEligible)
}

func fatalContextEval(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
