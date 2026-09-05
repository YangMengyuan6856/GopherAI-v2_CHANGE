package evaluation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildUnifiedEvaluationReportKeepsTechnicalAndHumanGatesSeparate(t *testing.T) {
	root := filepath.Join("..", "..")
	load := func(path string, target any) []byte {
		encoded, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, target); err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	var intent IntentCascadeReport
	var rag RAGReport
	var diagnosis DiagnosticEvaluationReport
	var tool ToolEvaluationReport
	var memory MemoryEvaluationReport
	intentBytes := load("evals/reports/devsupport-intent-cascade-latest.json", &intent)
	ragBytes := load("evals/reports/devsupport-rag-core-latest.json", &rag)
	diagnosisBytes := load("evals/results/devsupport-diagnostic-v1-candidate.json", &diagnosis)
	toolBytes := load("evals/results/devsupport-tool-runtime-v1-candidate.json", &tool)
	memoryBytes := load("evals/results/devsupport-memory-v1-candidate.json", &memory)
	catalog, err := ValidateEvalCatalogFile(filepath.Join(root, "evals/devsupport-eval-v1.manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := BuildUnifiedEvaluationReport(UnifiedEvaluationInput{
		CandidateVersion: "test-candidate", GeneratedAt: time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC), Catalog: catalog,
		Artifacts: []EvaluationArtifact{
			NewEvaluationArtifact("intent", intent.DatasetVersion, intentBytes, intent.CaseCount),
			NewEvaluationArtifact("rag", rag.DatasetVersion, ragBytes, rag.CaseCount),
			NewEvaluationArtifact("diagnosis", diagnosis.DatasetVersion, diagnosisBytes, diagnosis.Metrics.CaseCount),
			NewEvaluationArtifact("tool", tool.DatasetVersion, toolBytes, tool.Metrics.CaseCount),
			NewEvaluationArtifact("memory", memory.DatasetVersion, memoryBytes, memory.Metrics.CaseCount),
		}, Intent: intent, RAG: rag, Diagnostic: diagnosis, Tool: tool, Memory: memory,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Coverage.CatalogCases != 320 || report.Coverage.ExecutableCases != 300 || report.Coverage.CatalogOnlyCases != 20 || report.Coverage.ExecutionCoverage != .9375 {
		t.Fatalf("unexpected coverage: %+v", report.Coverage)
	}
	if !report.Decision.TechnicalGatesPassed || report.Decision.HumanReviewed || report.Decision.BaselineEligible || report.Decision.DefaultTrafficEligible || report.Decision.Status != "technical_candidate" {
		t.Fatalf("unexpected decision: %+v", report.Decision)
	}
	if len(report.RunID) != len("evalrun-")+16 || len(report.FailureClusters) == 0 {
		t.Fatalf("run identity or clusters missing: %+v", report)
	}
	var markdown strings.Builder
	if err := WriteUnifiedEvaluationMarkdown(&markdown, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markdown.String(), "300") || !strings.Contains(markdown.String(), "93.75%") {
		t.Fatalf("markdown omits coverage: %s", markdown.String())
	}
}
