package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"GopherAI/internal/evaluation"
	memorydomain "GopherAI/internal/memory"
	"GopherAI/internal/profilememory"
)

func main() {
	datasetPath := flag.String("dataset", "evals/devsupport-memory-v1.jsonl", "memory JSONL dataset")
	reportPath := flag.String("report", "evals/results/devsupport-memory-v1-candidate.json", "output report")
	flag.Parse()

	dataset, err := os.Open(*datasetPath)
	if err != nil {
		fatalMemoryEval(err)
	}
	defer dataset.Close()
	cases, summary, err := evaluation.LoadMemoryDataset(dataset)
	if err != nil {
		fatalMemoryEval(err)
	}
	report := evaluation.EvaluateMemory(profilememory.NewSelector(), memorydomain.NewAssembler(), cases, summary, time.Now())
	encoded, err := evaluation.MarshalMemoryReport(report)
	if err != nil {
		fatalMemoryEval(err)
	}
	if err := os.MkdirAll(filepath.Dir(*reportPath), 0o755); err != nil {
		fatalMemoryEval(err)
	}
	if err := os.WriteFile(*reportPath, append(encoded, '\n'), 0o644); err != nil {
		fatalMemoryEval(err)
	}
	fmt.Printf("memory eval: cases=%d recall=%.4f stale=%.4f deleted=%d leakage=%d budget=%.4f deterministic=%.4f technical_gates=%v baseline_eligible=%v\n",
		report.Metrics.CaseCount, report.Metrics.RelevantMemoryRecall, report.Metrics.StaleWrongInjectionRate,
		report.Metrics.DeletedMemoryRecall, report.Metrics.CrossPrincipalLeakage, report.Metrics.ContextBudgetPassRate,
		report.Metrics.DeterministicReplayRate, report.TechnicalGatesPassed, report.BaselineEligible)
}

func fatalMemoryEval(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
