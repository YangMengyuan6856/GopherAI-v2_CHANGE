package evaluation

import (
	"math"
	"os"
	"testing"
	"time"
)

func TestEvaluateRegressionBlocksRelativeQualityDrop(t *testing.T) {
	baseline := regressionFixture("baseline-v1", 0.95)
	candidate := regressionFixture("candidate-v2", 0.89)

	report, err := EvaluateRegression(baseline, candidate, time.Date(2026, 9, 5, 5, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("EvaluateRegression() error = %v", err)
	}
	if !report.Blocked || len(report.Metrics) != 2 {
		t.Fatalf("expected blocked two-metric report, got %+v", report)
	}
	quality := report.Metrics[0]
	if quality.Name != "accuracy" || quality.RelativePassed || !quality.AbsolutePassed {
		t.Fatalf("quality gate did not isolate relative regression: %+v", quality)
	}
	if math.Abs(quality.RelativeRegression-(0.06/0.95)) > 1e-9 {
		t.Fatalf("relative regression = %f", quality.RelativeRegression)
	}
}

func TestEvaluateRegressionAllowsDropWithinFivePercent(t *testing.T) {
	baseline := regressionFixture("baseline-v1", 0.95)
	candidate := regressionFixture("candidate-v2", 0.91)

	report, err := EvaluateRegression(baseline, candidate, time.Now())
	if err != nil {
		t.Fatalf("EvaluateRegression() error = %v", err)
	}
	if report.Blocked {
		t.Fatalf("unexpected gate failure: %+v", report.GateFailures)
	}
}

func TestEvaluateRegressionBlocksSafetyRegression(t *testing.T) {
	baseline := regressionFixture("baseline-v1", 0.95)
	candidate := regressionFixture("candidate-v2", 0.95)
	candidate.Metrics[1].Value = 1

	report, err := EvaluateRegression(baseline, candidate, time.Now())
	if err != nil {
		t.Fatalf("EvaluateRegression() error = %v", err)
	}
	if !report.Blocked || report.Metrics[1].SafetyPassed {
		t.Fatalf("safety regression must block: %+v", report)
	}
}

func TestEvaluateRegressionFailsClosedOnVersionAndReview(t *testing.T) {
	baseline := regressionFixture("baseline-v1", 0.95)
	candidate := regressionFixture("candidate-v2", 0.95)
	candidate.VersionLock.PromptVersion = "prompt-v2"
	candidate.HumanReviewed = false
	candidate.CompletionRate = 0.97

	report, err := EvaluateRegression(baseline, candidate, time.Now())
	if err != nil {
		t.Fatalf("EvaluateRegression() error = %v", err)
	}
	if !report.Blocked || report.VersionLockMatched || report.CompletionPassed || report.CandidateReviewed {
		t.Fatalf("fail-closed gates not enforced: %+v", report)
	}
}

func TestFileBaselineStoreFreezesReviewedSnapshotsImmutably(t *testing.T) {
	store, err := NewFileBaselineStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileBaselineStore() error = %v", err)
	}
	snapshot := regressionFixture("release-2026-09-05", 0.95)
	digest, err := store.Freeze(snapshot)
	if err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	if len(digest) != 64 {
		t.Fatalf("digest length = %d", len(digest))
	}
	if _, err := store.Freeze(snapshot); err == nil {
		t.Fatal("second Freeze() must reject immutable baseline overwrite")
	}
	loaded, loadedDigest, err := store.Load(snapshot.SnapshotID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.SnapshotID != snapshot.SnapshotID || loadedDigest != digest {
		t.Fatalf("loaded snapshot mismatch: %+v %s", loaded, loadedDigest)
	}
}

func TestFileBaselineStoreRejectsUnreviewedSnapshot(t *testing.T) {
	directory := t.TempDir()
	store, err := NewFileBaselineStore(directory)
	if err != nil {
		t.Fatalf("NewFileBaselineStore() error = %v", err)
	}
	snapshot := regressionFixture("unreviewed", 0.95)
	snapshot.HumanReviewed = false
	if _, err := store.Freeze(snapshot); err == nil {
		t.Fatal("Freeze() must reject unreviewed snapshot")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("rejected baseline left files behind: %+v", entries)
	}
}

func regressionFixture(snapshotID string, accuracy float64) EvaluationSnapshot {
	return EvaluationSnapshot{
		SchemaVersion:    BaselineSnapshotVersion,
		SnapshotID:       snapshotID,
		CandidateVersion: snapshotID,
		CreatedAt:        time.Date(2026, 9, 5, 5, 0, 0, 0, time.UTC),
		VersionLock: EvaluationVersionLock{
			DatasetVersion: "devsupport-eval-v1",
			DatasetSHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			FixtureSHA256:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			ModelVersion:   "qwen-plus",
			PromptVersion:  "answer-v1",
			JudgeVersion:   "judge-v1",
			Environment:    "test",
		},
		CaseCount:            320,
		CompletionRate:       1,
		TechnicalGatesPassed: true,
		HumanReviewed:        true,
		Metrics: []SnapshotMetric{
			{Name: "accuracy", Slice: "intent", Value: accuracy, Direction: MetricHigherIsBetter, AbsoluteTarget: 0.85, Critical: true},
			{Name: "unauthorized_recall", Slice: "rag", Value: 0, Direction: MetricMustBeZero, AbsoluteTarget: 0, Critical: true, SafetyCritical: true},
		},
	}
}
