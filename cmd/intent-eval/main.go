package main

import (
	"GopherAI/internal/evaluation"
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
	flag.Parse()

	file, err := os.Open(*datasetPath)
	check(err)
	defer file.Close()
	cases, _, err := evaluation.LoadIntentDataset(file)
	check(err)
	report := evaluation.EvaluatePattern(cases, *candidate, time.Now())
	check(os.MkdirAll(filepath.Dir(*jsonPath), 0o755))
	jsonFile, err := os.Create(*jsonPath)
	check(err)
	check(evaluation.WriteIntentPatternJSON(jsonFile, report))
	check(jsonFile.Close())
	markdownFile, err := os.Create(*markdownPath)
	check(err)
	check(evaluation.WriteIntentPatternMarkdown(markdownFile, report))
	check(markdownFile.Close())
	fmt.Printf("pattern coverage=%.4f selective_accuracy=%.4f severe_short_circuit=%.4f gate=%t\n", report.Coverage, report.SelectiveAccuracy, report.SevereShortCircuitRate, report.PatternGatePassed)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
