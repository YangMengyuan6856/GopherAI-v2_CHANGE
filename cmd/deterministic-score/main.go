package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"GopherAI/internal/evaluation"
)

func main() {
	intentPath := flag.String("intent", "evals/reports/devsupport-intent-cascade-latest.json", "intent report")
	ragPath := flag.String("rag", "evals/reports/devsupport-rag-core-latest.json", "RAG report")
	diagnosticPath := flag.String("diagnosis", "evals/results/devsupport-diagnostic-v1-candidate.json", "diagnostic report")
	toolPath := flag.String("tool", "evals/results/devsupport-tool-runtime-v1-candidate.json", "tool report")
	memoryPath := flag.String("memory", "evals/results/devsupport-memory-v1-candidate.json", "memory report")
	candidate := flag.String("candidate", "mixed-candidate-reports", "scorecard candidate identifier")
	outPath := flag.String("out", "", "optional output JSON path")
	flag.Parse()

	intentReport, err := loadJSON[evaluation.IntentCascadeReport](*intentPath)
	if err != nil {
		fatal(err)
	}
	ragReport, err := loadJSON[evaluation.RAGReport](*ragPath)
	if err != nil {
		fatal(err)
	}
	diagnosticReport, err := loadJSON[evaluation.DiagnosticEvaluationReport](*diagnosticPath)
	if err != nil {
		fatal(err)
	}
	toolReport, err := loadJSON[evaluation.ToolEvaluationReport](*toolPath)
	if err != nil {
		fatal(err)
	}
	memoryReport, err := loadJSON[evaluation.MemoryEvaluationReport](*memoryPath)
	if err != nil {
		fatal(err)
	}
	card, err := evaluation.BuildDeterministicScorecard(*candidate, time.Now(), intentReport, ragReport, diagnosticReport, toolReport, memoryReport)
	if err != nil {
		fatal(err)
	}
	encoded, err := json.MarshalIndent(card, "", "  ")
	if err != nil {
		fatal(err)
	}
	encoded = append(encoded, '\n')
	if *outPath == "" {
		_, _ = os.Stdout.Write(encoded)
	} else if err := os.WriteFile(*outPath, encoded, 0o644); err != nil {
		fatal(err)
	}
	if !card.TechnicalGatesPassed {
		os.Exit(2)
	}
}

func loadJSON[T any](path string) (T, error) {
	var result T
	file, err := os.Open(path)
	if err != nil {
		return result, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 32<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, fmt.Errorf("decode %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return result, fmt.Errorf("decode %s: trailing content", path)
	}
	return result, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
