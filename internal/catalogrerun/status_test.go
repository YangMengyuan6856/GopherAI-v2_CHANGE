package catalogrerun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInspectorDistinguishesNotStartedRunningAndCompleted(t *testing.T) {
	plan := newTestPlan(t)
	inspector := NewInspector(plan.OutputRoot)
	status, err := inspector.Status(context.Background(), plan.SealID)
	if err != nil || status.State != "not_started" || status.LatestRun != nil || status.EvidenceIntegrityVerified {
		t.Fatalf("unexpected not-started status: err=%v status=%+v", err, status)
	}
	base := filepath.Join(plan.OutputRoot, plan.SealID)
	if err := os.MkdirAll(base, 0o750); err != nil {
		t.Fatal(err)
	}
	activeRunID := "rerun-20260907T010203.000000004Z-abcdef12"
	if err := os.WriteFile(filepath.Join(base, "active.lock"), []byte(activeRunID+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	status, err = inspector.Status(context.Background(), plan.SealID)
	if err != nil || status.State != "running" || status.ActiveRunID != activeRunID || status.EvidenceIntegrityVerified {
		t.Fatalf("unexpected running status: err=%v status=%+v", err, status)
	}
	if err := os.Remove(filepath.Join(base, "active.lock")); err != nil {
		t.Fatal(err)
	}
	report, _, err := Execute(context.Background(), plan, &fakeExecutor{gateFailAt: map[string]int{}}, func() time.Time {
		return time.Date(2026, 9, 7, 2, 3, 4, 5, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err = inspector.Status(context.Background(), plan.SealID)
	if err != nil || status.State != "technical_passed" || !status.EvidenceIntegrityVerified || status.LatestRun == nil || status.LatestRun.ReportSHA256 != report.ReportSHA256 {
		t.Fatalf("unexpected completed status: err=%v status=%+v", err, status)
	}
}

func TestInspectorExposesFailedRunWithoutPromoting(t *testing.T) {
	plan := newTestPlan(t)
	inspector := NewInspector(plan.OutputRoot)
	report, _, err := Execute(context.Background(), plan, &fakeExecutor{failAt: "diagnosis", gateFailAt: map[string]int{}}, func() time.Time {
		return time.Date(2026, 9, 7, 3, 4, 5, 6, time.UTC)
	})
	if !errors.Is(err, ErrStepFailed) || report.PromotionEligible {
		t.Fatalf("expected controlled failed run: err=%v report=%+v", err, report)
	}
	status, err := inspector.Status(context.Background(), plan.SealID)
	if err != nil || status.State != "execution_failed" || !status.EvidenceIntegrityVerified || status.LatestRun == nil || status.LatestRun.FailureStep != "diagnosis" {
		t.Fatalf("unexpected failed status: err=%v status=%+v", err, status)
	}
}

func TestInspectorFailsClosedOnTamperedLatestEvidence(t *testing.T) {
	plan := newTestPlan(t)
	_, runDirectory, err := Execute(context.Background(), plan, &fakeExecutor{gateFailAt: map[string]int{}}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDirectory, "intent.json"), []byte("tampered\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := NewInspector(plan.OutputRoot).Status(context.Background(), plan.SealID); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("tampered latest run did not fail closed: %v", err)
	}
}
