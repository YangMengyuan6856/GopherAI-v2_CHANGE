package catalogrerun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/catalogreview"
	"GopherAI/internal/catalogseal"
)

type sealedReviewSource struct {
	snapshot catalogreview.Snapshot
	progress catalogreview.Progress
}

func (source sealedReviewSource) SealSnapshot(context.Context, string) (catalogreview.Snapshot, catalogreview.Progress, error) {
	return source.snapshot, source.progress, nil
}

type fakeExecutor struct {
	calls      []string
	failAt     string
	gateFailAt map[string]int
}

func (executor *fakeExecutor) Run(_ context.Context, binary string, arguments []string, _ string, stdout, stderr io.Writer) ExecutionResult {
	id := fakeStepID(filepath.Base(binary))
	executor.calls = append(executor.calls, id)
	fmt.Fprintln(stdout, "completed", id)
	if id == executor.failAt {
		fmt.Fprintln(stderr, "controlled failure")
		return ExecutionResult{ExitCode: 7, Err: errors.New("controlled failure")}
	}
	for index, argument := range arguments {
		if index == 0 || !fakeOutputFlag(arguments[index-1]) {
			continue
		}
		if err := os.WriteFile(filepath.FromSlash(argument), []byte("{\"step\":\""+id+"\"}\n"), 0o640); err != nil {
			return ExecutionResult{ExitCode: 125, Err: err}
		}
	}
	if code := executor.gateFailAt[id]; code != 0 {
		fmt.Fprintln(stderr, "technical gate failed")
		return ExecutionResult{ExitCode: code, Err: errors.New("technical gate failed")}
	}
	return ExecutionResult{ExitCode: 0}
}

func TestBuildPlanBindsSealReleaseAndBinaryHashes(t *testing.T) {
	plan := newTestPlan(t)
	if len(plan.Steps) != 6 || plan.Steps[0].ID != "intent" || plan.Steps[5].ID != "unified" || len(plan.PlanSHA256) != 64 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if err := ValidatePlan(plan); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	tampered := plan
	tampered.Steps = append([]Step(nil), plan.Steps...)
	tampered.Steps[0].Arguments = append([]string(nil), plan.Steps[0].Arguments...)
	tampered.Steps[0].Arguments[0] = "--arbitrary-shell-like-override"
	if err := ValidatePlan(tampered); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("tampered plan was accepted: %v", err)
	}
	if _, err := BuildPlan(plan.ArtifactRoot, filepath.Join(plan.ArtifactRoot, "outputs"), plan.WorkingDirectory, plan.ReleaseManifestPath); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("output inside immutable artifact was accepted: %v", err)
	}
	if _, err := BuildPlan(plan.ArtifactRoot, filepath.Join(plan.WorkingDirectory, "outputs"), plan.WorkingDirectory, plan.ReleaseManifestPath); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("output inside immutable release was accepted: %v", err)
	}
}

func TestExecuteWritesAppendOnlyCheckpointsAndNeverPromotes(t *testing.T) {
	plan := newTestPlan(t)
	executor := &fakeExecutor{gateFailAt: map[string]int{}}
	clock := func() time.Time { return time.Date(2026, 9, 7, 4, 5, 6, 7, time.UTC) }
	report, runDirectory, err := Execute(context.Background(), plan, executor, clock)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "completed_technical_pass" || !report.TechnicalGatePassed || report.PromotionEligible || len(report.Steps) != 6 || report.Checkpoint != 7 {
		t.Fatalf("unexpected successful report: %+v", report)
	}
	if strings.Join(executor.calls, ",") != "intent,rag,diagnosis,tool,memory,unified" {
		t.Fatalf("fixed order changed: %v", executor.calls)
	}
	for _, name := range []string{"plan.json", "checkpoint-00.json", "checkpoint-01.json", "checkpoint-06.json", "checkpoint-07.json", "result.json"} {
		if info, statErr := os.Stat(filepath.Join(runDirectory, name)); statErr != nil || !info.Mode().IsRegular() {
			t.Fatalf("missing run evidence %s: %v", name, statErr)
		}
	}
	if err := ValidateReport(report); err != nil {
		t.Fatalf("final report invalid: %v", err)
	}
	if verified, err := ValidateRunDirectory(runDirectory); err != nil || verified.ReportSHA256 != report.ReportSHA256 {
		t.Fatalf("physical run evidence invalid: err=%v report=%+v", err, verified)
	}
	if _, err := os.Stat(filepath.Join(plan.OutputRoot, plan.SealID, "active.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("active lock was not released: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDirectory, "intent.json"), []byte("tampered\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRunDirectory(runDirectory); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("tampered physical evidence was accepted: %v", err)
	}
}

func TestExecuteStopsOnFailureAndPreservesEvidence(t *testing.T) {
	plan := newTestPlan(t)
	executor := &fakeExecutor{failAt: "diagnosis", gateFailAt: map[string]int{}}
	report, runDirectory, err := Execute(context.Background(), plan, executor, func() time.Time { return time.Date(2026, 9, 7, 5, 0, 0, 0, time.UTC) })
	if !errors.Is(err, ErrStepFailed) || report.Status != "failed" || report.FailureStep != "diagnosis" || len(report.Steps) != 3 || report.PromotionEligible {
		t.Fatalf("unexpected failure report: err=%v report=%+v", err, report)
	}
	if strings.Join(executor.calls, ",") != "intent,rag,diagnosis" {
		t.Fatalf("steps continued after failure: %v", executor.calls)
	}
	if _, statErr := os.Stat(filepath.Join(runDirectory, "result.json")); statErr != nil {
		t.Fatalf("failure evidence was not preserved: %v", statErr)
	}
}

func TestTechnicalGateFailureCompletesButDoesNotPass(t *testing.T) {
	plan := newTestPlan(t)
	executor := &fakeExecutor{gateFailAt: map[string]int{"rag": 1, "unified": 2}}
	report, _, err := Execute(context.Background(), plan, executor, func() time.Time { return time.Date(2026, 9, 7, 6, 0, 0, 0, time.UTC) })
	if err != nil || report.Status != "completed_technical_gate_failed" || report.TechnicalGatePassed || report.PromotionEligible || len(report.Steps) != 6 {
		t.Fatalf("technical failure semantics changed: err=%v report=%+v", err, report)
	}
	if err := ValidateReport(report); err != nil {
		t.Fatalf("gate-failed report invalid: %v", err)
	}
}

func TestExecuteRejectsConcurrentSealRun(t *testing.T) {
	plan := newTestPlan(t)
	base := filepath.Join(plan.OutputRoot, plan.SealID)
	if err := os.MkdirAll(base, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "active.lock"), []byte("existing\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Execute(context.Background(), plan, &fakeExecutor{}, time.Now); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent run was accepted: %v", err)
	}
}

func newTestPlan(t *testing.T) Plan {
	t.Helper()
	repositoryRoot := filepath.Join("..", "..")
	catalogPath := filepath.Join(repositoryRoot, catalogseal.DefaultCatalogPath)
	reviewPath := filepath.Join(repositoryRoot, catalogseal.DefaultReviewManifestPath)
	snapshot, err := catalogreview.NewFileArtifactStore(catalogPath).Load()
	if err != nil {
		t.Fatal(err)
	}
	progress := catalogreview.Progress{Total: 320, Reviewed: 320, Approved: 320, ReviewSetSHA256: strings.Repeat("a", 64), ReadyForMaterializing: true}
	sealRoot := t.TempDir()
	service := catalogseal.NewService(sealedReviewSource{snapshot: snapshot, progress: progress}, catalogPath, reviewPath, sealRoot, func() time.Time {
		return time.Date(2026, 9, 7, 1, 0, 0, 0, time.UTC)
	})
	receipt, err := service.Seal(context.Background(), "reviewer", catalogseal.Command{CatalogSHA256: snapshot.CatalogSHA256, ReviewSetSHA256: progress.ReviewSetSHA256, Acknowledgment: catalogseal.Acknowledgment})
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := filepath.Join(sealRoot, receipt.Report.SealID)
	workingDirectory := t.TempDir()
	for _, binary := range []string{"GopherAI-intent-eval", "GopherAI-rag-eval", "GopherAI-diagnostic-eval", "GopherAI-tool-eval", "GopherAI-memory-eval", "GopherAI-eval-runner"} {
		if err := os.WriteFile(filepath.Join(workingDirectory, binary), []byte("fixed test binary "+binary+"\n"), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	manifest := ReleaseManifest{
		ReleaseID: "20260907010000-abcdef123456", Branch: "add_eico", GitSHA: strings.Repeat("b", 40),
		BuiltAt: time.Date(2026, 9, 7, 1, 0, 0, 0, time.UTC), BuildStrategy: "local-linux-amd64-nocgo", Target: "linux/amd64",
		GoVersion: "go1.25", GoBuildFlags: []string{"-p=1"}, IncludedComponents: requiredComponents(), Rollback: "previous-directory",
	}
	encoded, _ := json.Marshal(manifest)
	manifestPath := filepath.Join(workingDirectory, "release-manifest.json")
	if err := os.WriteFile(manifestPath, append(encoded, '\n'), 0o640); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(artifactRoot, t.TempDir(), workingDirectory, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func fakeOutputFlag(value string) bool {
	switch value {
	case "-json", "-markdown", "-out-json", "-out-md", "-report":
		return true
	default:
		return false
	}
}

func fakeStepID(binary string) string {
	return map[string]string{
		"GopherAI-intent-eval": "intent", "GopherAI-rag-eval": "rag", "GopherAI-diagnostic-eval": "diagnosis",
		"GopherAI-tool-eval": "tool", "GopherAI-memory-eval": "memory", "GopherAI-eval-runner": "unified",
	}[binary]
}
