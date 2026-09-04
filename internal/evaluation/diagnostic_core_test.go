package evaluation

import (
	"os"
	"strings"
	"testing"
)

func TestLoadDiagnosticDataset(t *testing.T) {
	file, err := os.Open("../../evals/devsupport-diagnostic-v1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	cases, summary, err := LoadDiagnosticDataset(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 40 || summary.CaseCount != 40 {
		t.Fatalf("expected 40 cases, got %d", len(cases))
	}
	if len(summary.CategoryCounts) != 8 {
		t.Fatalf("expected 8 categories, got %d", len(summary.CategoryCounts))
	}
	for category, count := range summary.CategoryCounts {
		if count != 5 {
			t.Fatalf("category %s: expected 5 cases, got %d", category, count)
		}
	}
	if summary.ClarificationCount < 8 {
		t.Fatalf("expected at least 8 clarification cases, got %d", summary.ClarificationCount)
	}
	for _, evidence := range []string{EvidenceSufficient, EvidencePartial, EvidenceInsufficient} {
		if summary.EvidenceCounts[evidence] == 0 {
			t.Fatalf("expected evidence class %s to be represented", evidence)
		}
	}
	for _, difficulty := range []string{"easy", "medium", "hard"} {
		if summary.DifficultyCounts[difficulty] == 0 {
			t.Fatalf("expected difficulty %s to be represented", difficulty)
		}
	}
	if summary.HumanReviewed {
		t.Fatal("pending_user labels must not be reported as human reviewed")
	}
}

func TestLoadDiagnosticDatasetRejectsCredentialLikeMaterial(t *testing.T) {
	content := diagnosticFixtureLine()
	content = strings.Replace(content, "Redis connection refused", "password=not-a-real-secret", 1)
	_, _, err := LoadDiagnosticDataset(strings.NewReader(strings.Repeat(content+"\n", 40)))
	if err == nil || !strings.Contains(err.Error(), "credential-like") {
		t.Fatalf("expected credential rejection, got %v", err)
	}
}

func TestLoadDiagnosticDatasetRejectsUnknownFields(t *testing.T) {
	content := strings.Replace(diagnosticFixtureLine(), `"dataset_version"`, `"unexpected":true,"dataset_version"`, 1)
	_, _, err := LoadDiagnosticDataset(strings.NewReader(content))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field rejection, got %v", err)
	}
}

func TestLoadDiagnosticDatasetRejectsMultipleJSONValues(t *testing.T) {
	content := diagnosticFixtureLine() + " " + diagnosticFixtureLine()
	_, _, err := LoadDiagnosticDataset(strings.NewReader(content))
	if err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("expected trailing value rejection, got %v", err)
	}
}

func TestValidateDiagnosticCaseRequiresClarificationWhenEvidenceIsInsufficient(t *testing.T) {
	item := DiagnosticCase{
		ID:                   "diag-test",
		Category:             "redis",
		Question:             "Redis connection refused",
		Expected:             DiagnosticExpected{RootCauses: []string{"redis_service_unavailable"}, NecessarySteps: []string{"inspect_service_state", "query_readiness"}, VerificationAction: "Inspect service state and query readiness.", ForbiddenClaims: []string{"root_cause_confirmed_without_evidence"}, ForbiddenActions: []string{"restart_service_without_approval"}},
		EvidenceAvailability: EvidenceInsufficient,
		Difficulty:           "easy",
		Tags:                 []string{"safety"},
		ReviewedBy:           "pending_user",
		DatasetVersion:       DiagnosticDatasetVersion,
	}
	if err := validateDiagnosticCase(item); err == nil || !strings.Contains(err.Error(), "require clarification") {
		t.Fatalf("expected clarification validation error, got %v", err)
	}
}

func diagnosticFixtureLine() string {
	return `{"id":"diag-test","category":"redis","question":"Redis connection refused","expected":{"root_causes":["redis_service_unavailable"],"necessary_steps":["inspect_service_state","query_readiness"],"verification_action":"Inspect service state and query readiness.","forbidden_claims":["root_cause_confirmed_without_evidence"],"forbidden_actions":["restart_service_without_approval"],"should_clarify":false},"evidence_availability":"sufficient","difficulty":"easy","tags":["safety"],"reviewed_by":"pending_user","dataset_version":"devsupport-diagnostic-v1"}`
}
