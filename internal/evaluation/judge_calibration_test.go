package evaluation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"
)

type calibrationJudgeStub struct{ score float64 }

func (stub calibrationJudgeStub) Judge(_ context.Context, input JudgeInput) (JudgeResult, error) {
	scores := JudgeScores{Relevance: stub.score, Completeness: stub.score, Helpfulness: stub.score, Groundedness: stub.score, Safety: stub.score}
	return JudgeResult{SchemaVersion: "judge-result-v1", AdapterVersion: JudgeAdapterVersion, PromptVersion: JudgePromptVersion, ModelVersion: "judge-model", Status: JudgeStatusComplete, Attempts: 1, Scores: scores, Overall: weightedJudgeOverall(scores), Confidence: 1}, nil
}

func TestJudgeCalibrationDatasetIsBalancedAndFrozen(t *testing.T) {
	content, err := os.ReadFile("../../evals/devsupport-judge-calibration-v1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	cases, err := LoadJudgeCalibrationCases(bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 30 || len(SortedJudgeCalibrationSlices()) != 6 {
		t.Fatalf("unexpected calibration catalog: cases=%d slices=%v", len(cases), SortedJudgeCalibrationSlices())
	}
}

func TestJudgeCalibrationAgreementFailsClosedUntilThirtyHumanRatings(t *testing.T) {
	report := calibrationReport(t)
	ratings := []HumanCalibrationRating{{CaseID: report.Cases[0].ID, Scores: report.Cases[0].Scores}}
	agreement, err := AnalyzeCalibrationAgreement(report, ratings)
	if err != nil {
		t.Fatal(err)
	}
	if agreement.Status != "insufficient_human_review" || agreement.ReviewedCases != 1 || agreement.CalibrationGatePassed || agreement.AutomationUsePermitted {
		t.Fatalf("partial review must fail closed: %+v", agreement)
	}
}

func TestJudgeCalibrationAgreementPassesPerfectThirtyCaseReview(t *testing.T) {
	report := calibrationReport(t)
	ratings := make([]HumanCalibrationRating, 0, len(report.Cases))
	for _, item := range report.Cases {
		ratings = append(ratings, HumanCalibrationRating{CaseID: item.ID, Scores: item.Scores})
	}
	agreement, err := AnalyzeCalibrationAgreement(report, ratings)
	if err != nil {
		t.Fatal(err)
	}
	if agreement.Status != "calibrated" || agreement.LinearWeightedKappa != 1 || !agreement.CalibrationGatePassed || !agreement.AutomationUsePermitted {
		t.Fatalf("perfect review must pass: %+v", agreement)
	}
}

func calibrationReport(t *testing.T) JudgeCalibrationReport {
	t.Helper()
	content, err := os.ReadFile("../../evals/devsupport-judge-calibration-v1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	cases, err := LoadJudgeCalibrationCases(bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	report, err := RunJudgeCalibration(context.Background(), cases, hex.EncodeToString(digest[:]), time.Unix(1, 0), calibrationJudgeStub{score: .75})
	if err != nil {
		t.Fatal(err)
	}
	return report
}
