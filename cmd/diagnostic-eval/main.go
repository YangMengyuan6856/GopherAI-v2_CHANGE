package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"GopherAI/internal/diagnostic"
	"GopherAI/internal/evaluation"
)

func main() {
	datasetPath := flag.String("dataset", "evals/devsupport-diagnostic-v1.jsonl", "diagnostic JSONL dataset")
	reportPath := flag.String("report", "evals/results/devsupport-diagnostic-v1-candidate.json", "output report")
	flag.Parse()

	dataset, err := os.Open(*datasetPath)
	if err != nil {
		fatal(err)
	}
	defer dataset.Close()
	cases, summary, err := evaluation.LoadDiagnosticDataset(dataset)
	if err != nil {
		fatal(err)
	}
	report, err := evaluation.EvaluateDiagnosticAgent(diagnostic.NewAgent(), cases, summary, time.Now())
	if err != nil {
		fatal(err)
	}
	encoded, err := evaluation.MarshalDiagnosticReport(report)
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*reportPath), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*reportPath, append(encoded, '\n'), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("diagnostic eval: cases=%d top3=%.4f steps=%.4f verification=%.4f clarify=%.4f premature=%.4f dangerous=%.4f technical_gates=%v baseline_eligible=%v\n",
		report.Metrics.CaseCount, report.Metrics.RootCauseTop3Recall, report.Metrics.NecessaryStepCoverage,
		report.Metrics.VerificationActionAccuracy, report.Metrics.ClarificationAccuracy,
		report.Metrics.PrematureCertaintyRate, report.Metrics.DangerousActionRate,
		report.TechnicalGatesPassed, report.BaselineEligible)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
