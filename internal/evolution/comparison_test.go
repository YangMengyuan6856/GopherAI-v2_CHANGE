package evolution

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestFairComparisonRejectsNonBeneficialCandidateWithoutOpeningHoldout(t *testing.T) {
	base := filepath.Join("..", "..", "evals")
	report, reused, err := RunFairComparison(context.Background(), filepath.Join(base, "devsupport-diagnostic-v1.jsonl"), filepath.Join(base, "devsupport-eval-v1.manifest.json"), nil, time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	if reused || report.Promotion.Eligible || report.Promotion.Decision != "rejected" || report.Holdout.Opened || report.Holdout.OpenCount != 0 || report.Holdout.State != "sealed" || len(report.Splits) != 2 {
		t.Fatalf("comparison crossed a promotion or holdout boundary: %+v", report)
	}
	for _, split := range report.Splits {
		evolved := comparisonFor(split, VariantEvolved)
		if evolved.Analysis.MeanDelta >= 0 {
			t.Fatalf("controlled over-constrained candidate should not show a fabricated gain: %+v", evolved.Analysis)
		}
		if dangerousCount(split, VariantEvolved) != 0 {
			t.Fatalf("candidate introduced dangerous actions: %+v", split)
		}
	}
	reusedReport, reused, err := RunFairComparison(context.Background(), filepath.Join(base, "devsupport-diagnostic-v1.jsonl"), filepath.Join(base, "devsupport-eval-v1.manifest.json"), &report, time.Unix(300, 0))
	if err != nil || !reused || reusedReport.ReportSHA256 != report.ReportSHA256 || !reusedReport.GeneratedAt.Equal(report.GeneratedAt) {
		t.Fatalf("same experiment reopened instead of reusing report: reused=%v err=%v", reused, err)
	}
}

func TestComparisonStoreRejectsTampering(t *testing.T) {
	base := filepath.Join("..", "..", "evals")
	report, _, err := RunFairComparison(context.Background(), filepath.Join(base, "devsupport-diagnostic-v1.jsonl"), filepath.Join(base, "devsupport-eval-v1.manifest.json"), nil, time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	store := NewFileComparisonStore(filepath.Join(t.TempDir(), "comparison.json"))
	if err := store.Save(report); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.ReportSHA256 != report.ReportSHA256 {
		t.Fatalf("stored report did not round trip: %v", err)
	}
	report.Promotion.Eligible = true
	if err := store.Save(report); err == nil {
		t.Fatal("tampered promotion result was persisted")
	}
}
