package main

import (
	"GopherAI/internal/evaluation"
	intentplatform "GopherAI/internal/platform/intent"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func main() {
	datasetPath := flag.String("dataset", "evals/devsupport-intent-v1.jsonl", "intent JSONL dataset")
	jsonPath := flag.String("json", "evals/reports/devsupport-intent-pattern-latest.json", "JSON report output")
	markdownPath := flag.String("markdown", "evals/reports/devsupport-intent-pattern-latest.md", "Markdown report output")
	candidate := flag.String("candidate", "working-tree", "candidate git revision")
	mode := flag.String("mode", "pattern", "evaluation mode: pattern or cascade")
	flag.Parse()

	file, err := os.Open(*datasetPath)
	check(err)
	defer file.Close()
	cases, _, err := evaluation.LoadIntentDataset(file)
	check(err)
	check(os.MkdirAll(filepath.Dir(*jsonPath), 0o755))
	jsonFile, err := os.Create(*jsonPath)
	check(err)
	markdownFile, err := os.Create(*markdownPath)
	check(err)
	switch *mode {
	case "pattern":
		report := evaluation.EvaluatePattern(cases, *candidate, time.Now())
		check(evaluation.WriteIntentPatternJSON(jsonFile, report))
		check(evaluation.WriteIntentPatternMarkdown(markdownFile, report))
		fmt.Printf("pattern coverage=%.4f selective_accuracy=%.4f severe_short_circuit=%.4f gate=%t\n", report.Coverage, report.SelectiveAccuracy, report.SevereShortCircuitRate, report.PatternGatePassed)
	case "cascade":
		report := evaluation.EvaluateCascade(context.Background(), cases, intentplatform.NewDefaultRecognizer(), *candidate, time.Now())
		check(evaluation.WriteIntentCascadeJSON(jsonFile, report))
		check(evaluation.WriteIntentCascadeMarkdown(markdownFile, report))
		fmt.Printf("cascade accuracy=%.4f macro_f1=%.4f min_recall=%.4f severe=%.4f llm_rate=%.4f gate=%t baseline_eligible=%t\n", report.Accuracy, report.MacroF1, report.MinimumRecall, report.SevereMisrouteRate, report.LLMCallRate, report.TechnicalGatePassed, report.BaselineEligible)
	default:
		check(fmt.Errorf("unsupported mode %q", *mode))
	}
	check(jsonFile.Close())
	check(markdownFile.Close())
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
