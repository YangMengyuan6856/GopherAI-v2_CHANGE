package judgecalibration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"GopherAI/internal/evaluation"
	"GopherAI/model"
)

type artifactStoreStub struct {
	cases  []evaluation.JudgeCalibrationCase
	report evaluation.JudgeCalibrationReport
}

func (stub artifactStoreStub) Load() ([]evaluation.JudgeCalibrationCase, evaluation.JudgeCalibrationReport, error) {
	return stub.cases, stub.report, nil
}

type memoryReviewRepository struct {
	rows []model.JudgeCalibrationReview
}

func (repository *memoryReviewRepository) ListLatest(_ context.Context, datasetSHA, reviewerHash string) ([]model.JudgeCalibrationReview, error) {
	latest := map[string]model.JudgeCalibrationReview{}
	for _, row := range repository.rows {
		if row.DatasetSHA256 == datasetSHA && row.ReviewerHash == reviewerHash && row.Revision > latest[row.CaseID].Revision {
			latest[row.CaseID] = row
		}
	}
	result := make([]model.JudgeCalibrationReview, 0, len(latest))
	for _, row := range latest {
		result = append(result, row)
	}
	return result, nil
}

func (repository *memoryReviewRepository) Append(_ context.Context, review *model.JudgeCalibrationReview) (bool, model.JudgeCalibrationReview, error) {
	latest := model.JudgeCalibrationReview{}
	for _, row := range repository.rows {
		if row.DatasetSHA256 == review.DatasetSHA256 && row.CaseID == review.CaseID && row.ReviewerHash == review.ReviewerHash && row.Revision > latest.Revision {
			latest = row
		}
	}
	if latest.ReviewSHA256 == review.ReviewSHA256 {
		return false, latest, nil
	}
	review.Revision = latest.Revision + 1
	repository.rows = append(repository.rows, *review)
	return true, *review, nil
}

type calibrationJudgeStub struct{}

func (calibrationJudgeStub) Judge(_ context.Context, _ evaluation.JudgeInput) (evaluation.JudgeResult, error) {
	scores := evaluation.JudgeScores{Relevance: .75, Completeness: .75, Helpfulness: .75, Groundedness: .75, Safety: 1}
	return evaluation.JudgeResult{ModelVersion: "judge-model", Status: evaluation.JudgeStatusComplete, Attempts: 1, Scores: scores, Overall: .775, Confidence: .75}, nil
}

func TestServiceScopesAppendOnlyReviewsToCurrentReviewer(t *testing.T) {
	artifacts := calibrationArtifacts(t)
	repository := new(memoryReviewRepository)
	service := NewService(artifacts, repository, func() time.Time { return time.Unix(10, 0) })
	audit, err := service.Audit(context.Background(), "alice")
	if err != nil || audit.CaseCount != 30 || audit.Agreement.ReviewedCases != 0 || audit.Agreement.AutomationUsePermitted {
		t.Fatalf("unexpected initial audit: %+v err=%v", audit, err)
	}
	scores := evaluation.JudgeScores{Relevance: 1, Completeness: .75, Helpfulness: .5, Groundedness: 1, Safety: 1}
	first, err := service.Submit(context.Background(), "alice", artifacts.cases[0].ID, scores)
	if err != nil || !first.Created || first.Revision != 1 {
		t.Fatalf("unexpected first review: %+v err=%v", first, err)
	}
	duplicate, err := service.Submit(context.Background(), "alice", artifacts.cases[0].ID, scores)
	if err != nil || duplicate.Created || duplicate.Revision != 1 {
		t.Fatalf("same scores must be idempotent: %+v err=%v", duplicate, err)
	}
	corrected := scores
	corrected.Helpfulness = .75
	revision, err := service.Submit(context.Background(), "alice", artifacts.cases[0].ID, corrected)
	if err != nil || !revision.Created || revision.Revision != 2 {
		t.Fatalf("correction must append a revision: %+v err=%v", revision, err)
	}
	alice, _ := service.Audit(context.Background(), "alice")
	bob, _ := service.Audit(context.Background(), "bob")
	if alice.Agreement.ReviewedCases != 1 || alice.Cases[0].ReviewRevision != 2 || bob.Agreement.ReviewedCases != 0 {
		t.Fatalf("reviewer scope or latest revision failed: alice=%+v bob=%+v", alice.Agreement, bob.Agreement)
	}
}

func TestGovernedAuditExplainsEvidenceVariantsAndScoringRubric(t *testing.T) {
	artifacts := calibrationArtifacts(t)
	repository := new(memoryReviewRepository)
	service := NewGovernedService(
		artifacts,
		"../../evals/devsupport-eval-v1.governance.json",
		"../../evals/devsupport-eval-v1.manifest.json",
		repository,
		time.Now,
	)
	audit, err := service.Audit(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if audit.GovernanceVersion != "devsupport-eval-governance-v2-human-adjudicated" || len(audit.GovernanceSHA256) != 64 || len(audit.CalibrationCard.ScoreAnchors) != 5 || len(audit.CalibrationCard.Dimensions) != 5 {
		t.Fatalf("governed calibration card is incomplete: %+v", audit)
	}
	if len(audit.Cases) != 30 || audit.Cases[0].AnswerVariant != "" {
		t.Fatalf("answer variant leaked before independent human scoring: %+v", audit.Cases)
	}
	for _, item := range audit.Cases {
		for _, evidence := range item.Evidence {
			if evidence.SourceID == "" || evidence.Origin != "embedded_synthetic_evidence" {
				t.Fatalf("evidence provenance is missing: %+v", evidence)
			}
		}
	}
	allOne := evaluation.JudgeScores{Relevance: 1, Completeness: 1, Helpfulness: 1, Groundedness: 1, Safety: 1}
	if _, err := service.Submit(context.Background(), "alice", artifacts.cases[0].ID, allOne); err != nil {
		t.Fatal(err)
	}
	reviewed, err := service.Audit(context.Background(), "alice")
	if err != nil || reviewed.Cases[0].AnswerVariant == "" {
		t.Fatalf("answer variant was not revealed after scoring: %+v err=%v", reviewed.Cases[0], err)
	}
}

func TestServiceRejectsNonQuarterScoresAndUnknownCases(t *testing.T) {
	artifacts := calibrationArtifacts(t)
	service := NewService(artifacts, new(memoryReviewRepository), time.Now)
	invalid := evaluation.JudgeScores{Relevance: .3, Completeness: 1, Helpfulness: 1, Groundedness: 1, Safety: 1}
	if _, err := service.Submit(context.Background(), "alice", artifacts.cases[0].ID, invalid); err != ErrInvalidReview {
		t.Fatalf("expected score increment rejection, got %v", err)
	}
	valid := evaluation.JudgeScores{Relevance: 1, Completeness: 1, Helpfulness: 1, Groundedness: 1, Safety: 1}
	if _, err := service.Submit(context.Background(), "alice", "missing", valid); err != ErrCaseNotFound {
		t.Fatalf("expected unknown case rejection, got %v", err)
	}
}

func calibrationArtifacts(t *testing.T) artifactStoreStub {
	t.Helper()
	content, err := os.ReadFile("../../evals/devsupport-judge-calibration-v1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	cases, err := evaluation.LoadJudgeCalibrationCases(bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	report, err := evaluation.RunJudgeCalibration(context.Background(), cases, hex.EncodeToString(digest[:]), time.Unix(1, 0), calibrationJudgeStub{})
	if err != nil {
		t.Fatal(err)
	}
	for index := range report.Cases {
		encoded, _ := json.Marshal(cases[index])
		caseDigest := sha256.Sum256(encoded)
		report.Cases[index].CaseSHA256 = hex.EncodeToString(caseDigest[:])
	}
	if err := evaluation.FinalizeJudgeCalibrationReport(&report); err != nil {
		t.Fatal(err)
	}
	return artifactStoreStub{cases: cases, report: report}
}
