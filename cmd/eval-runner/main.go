package main

import (
	"bytes"
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
	manifestPath := flag.String("manifest", "evals/devsupport-eval-v1.manifest.json", "Full 320 evaluation catalog manifest")
	intentPath := flag.String("intent", "evals/reports/devsupport-intent-cascade-latest.json", "intent source report")
	ragPath := flag.String("rag", "evals/reports/devsupport-rag-core-latest.json", "RAG source report")
	diagnosticPath := flag.String("diagnosis", "evals/results/devsupport-diagnostic-v1-candidate.json", "diagnostic source report")
	toolPath := flag.String("tool", "evals/results/devsupport-tool-runtime-v1-candidate.json", "tool source report")
	memoryPath := flag.String("memory", "evals/results/devsupport-memory-v1-candidate.json", "memory source report")
	candidate := flag.String("candidate", "working-tree", "candidate identifier")
	jsonPath := flag.String("out-json", "evals/results/devsupport-eval-run-v1-candidate.json", "machine-readable report")
	markdownPath := flag.String("out-md", "evals/results/devsupport-eval-run-v1-candidate.md", "human-readable report")
	flag.Parse()

	catalog, err := evaluation.ValidateEvalCatalogFile(*manifestPath)
	if err != nil {
		fatal(err)
	}
	intent, intentBytes, err := loadJSON[evaluation.IntentCascadeReport](*intentPath)
	if err != nil {
		fatal(err)
	}
	rag, ragBytes, err := loadJSON[evaluation.RAGReport](*ragPath)
	if err != nil {
		fatal(err)
	}
	diagnosis, diagnosisBytes, err := loadJSON[evaluation.DiagnosticEvaluationReport](*diagnosticPath)
	if err != nil {
		fatal(err)
	}
	tool, toolBytes, err := loadJSON[evaluation.ToolEvaluationReport](*toolPath)
	if err != nil {
		fatal(err)
	}
	memory, memoryBytes, err := loadJSON[evaluation.MemoryEvaluationReport](*memoryPath)
	if err != nil {
		fatal(err)
	}

	report, err := evaluation.BuildUnifiedEvaluationReport(evaluation.UnifiedEvaluationInput{
		CandidateVersion: *candidate, GeneratedAt: time.Now(), Catalog: catalog,
		Artifacts: []evaluation.EvaluationArtifact{
			evaluation.NewEvaluationArtifact("intent", intent.DatasetVersion, intentBytes, intent.CaseCount),
			evaluation.NewEvaluationArtifact("rag", rag.DatasetVersion, ragBytes, rag.CaseCount),
			evaluation.NewEvaluationArtifact("diagnosis", diagnosis.DatasetVersion, diagnosisBytes, diagnosis.Metrics.CaseCount),
			evaluation.NewEvaluationArtifact("tool", tool.DatasetVersion, toolBytes, tool.Metrics.CaseCount),
			evaluation.NewEvaluationArtifact("memory", memory.DatasetVersion, memoryBytes, memory.Metrics.CaseCount),
		}, Intent: intent, RAG: rag, Diagnostic: diagnosis, Tool: tool, Memory: memory,
	})
	if err != nil {
		fatal(err)
	}
	if err := writeReports(report, *jsonPath, *markdownPath); err != nil {
		fatal(err)
	}
	fmt.Printf("unified eval: run=%s catalog=%d executable=%d completion=%.4f technical=%t human_reviewed=%t baseline_eligible=%t clusters=%d\n",
		report.RunID, report.Coverage.CatalogCases, report.Coverage.ExecutableCases, report.Coverage.CompletionRate,
		report.Decision.TechnicalGatesPassed, report.Decision.HumanReviewed, report.Decision.BaselineEligible, len(report.FailureClusters))
	if !report.Decision.TechnicalGatesPassed {
		os.Exit(2)
	}
}

func loadJSON[T any](path string) (T, []byte, error) {
	var result T
	encoded, err := os.ReadFile(path)
	if err != nil {
		return result, nil, fmt.Errorf("read %s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return result, nil, fmt.Errorf("decode %s: trailing content", path)
	}
	return result, encoded, nil
}

func writeReports(report evaluation.UnifiedEvaluationReport, jsonPath string, markdownPath string) error {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	markdown, err := os.Create(markdownPath)
	if err != nil {
		return err
	}
	writeErr := evaluation.WriteUnifiedEvaluationMarkdown(markdown, report)
	closeErr := markdown.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
