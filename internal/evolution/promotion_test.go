package evolution

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"GopherAI/model"
)

func TestPromotionReviewRecordsRejectionAndBlocksApprovalWithoutActivePointer(t *testing.T) {
	report := comparisonFixture(t)
	repository := &memoryPromotionRepository{}
	now := time.Unix(500, 0)
	service, err := NewPromotionService(staticComparisonStore{report: report}, repository, staticPromotionAuthorizer{allowed: true}, func() time.Time { now = now.Add(time.Second); return now })
	if err != nil {
		t.Fatal(err)
	}
	rejection := promotionCommand(report, PromotionDecisionReject, "candidate_no_measured_gain", "promotion-review-key-reject-0001")
	first, err := service.Review(context.Background(), "reviewer", rejection)
	if err != nil || !first.Created || first.Reused || first.Attempt.Outcome != PromotionOutcomeRecorded || first.Attempt.ActivePointerChanged || first.ActivePointers != 0 || first.AffectsLiveTraffic {
		t.Fatalf("rejection was not safely recorded: receipt=%+v err=%v", first, err)
	}
	reused, err := service.Review(context.Background(), "reviewer", rejection)
	if err != nil || reused.Created || !reused.Reused || reused.Attempt.ID != first.Attempt.ID {
		t.Fatalf("rejection was not idempotent: receipt=%+v err=%v", reused, err)
	}
	approval := promotionCommand(report, PromotionDecisionApprove, "human_approval_requested", "promotion-review-key-approve-0001")
	blocked, err := service.Review(context.Background(), "reviewer", approval)
	if err != nil || blocked.Attempt.Outcome != PromotionOutcomeBlocked || blocked.Attempt.ReasonCode != "controlled_fixture_not_promotable" || blocked.Attempt.ActivePointerChanged || blocked.ActivePointers != 0 {
		t.Fatalf("ineligible candidate crossed promotion gate: receipt=%+v err=%v", blocked, err)
	}
	audit, err := service.Audit(context.Background(), "reviewer")
	if err != nil || !audit.CanReview || audit.AttemptCount != 2 || audit.RecordedCount != 1 || audit.BlockedCount != 1 || audit.RejectedCount != 1 || audit.ActivePointers != 0 {
		t.Fatalf("unexpected promotion audit: audit=%+v err=%v", audit, err)
	}
}

func TestPromotionReviewRejectsUnauthorizedStaleAndConflictingRequests(t *testing.T) {
	report := comparisonFixture(t)
	command := promotionCommand(report, PromotionDecisionReject, "candidate_no_measured_gain", "promotion-review-key-conflict-001")
	denied, _ := NewPromotionService(staticComparisonStore{report: report}, &memoryPromotionRepository{}, staticPromotionAuthorizer{}, time.Now)
	if _, err := denied.Review(context.Background(), "viewer", command); !errors.Is(err, ErrPromotionPermissionDenied) {
		t.Fatalf("ordinary login was allowed to review: %v", err)
	}
	repository := &memoryPromotionRepository{}
	service, _ := NewPromotionService(staticComparisonStore{report: report}, repository, staticPromotionAuthorizer{allowed: true}, func() time.Time { return time.Unix(500, 0) })
	stale := command
	stale.ReportSHA256 = stringsOf("0", 64)
	if _, err := service.Review(context.Background(), "reviewer", stale); !errors.Is(err, ErrPromotionReportStale) {
		t.Fatalf("stale report binding was accepted: %v", err)
	}
	if _, err := service.Review(context.Background(), "reviewer", command); err != nil {
		t.Fatal(err)
	}
	conflicting := command
	conflicting.ReasonCode = "risk_not_acceptable"
	if _, err := service.Review(context.Background(), "reviewer", conflicting); !errors.Is(err, ErrPromotionIdempotency) {
		t.Fatalf("idempotency conflict was accepted: %v", err)
	}
}

func comparisonFixture(t *testing.T) ComparisonReport {
	t.Helper()
	base := filepath.Join("..", "..", "evals")
	report, _, err := RunFairComparison(context.Background(), filepath.Join(base, "devsupport-diagnostic-v1.jsonl"), filepath.Join(base, "devsupport-eval-v1.manifest.json"), nil, time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func promotionCommand(report ComparisonReport, decision, reason, key string) PromotionReviewCommand {
	return PromotionReviewCommand{ExperimentVersion: report.ExperimentVersion, CandidateSHA256: report.Candidate.ArtifactSHA256, ReportSHA256: report.ReportSHA256,
		Decision: decision, ReasonCode: reason, IdempotencyKey: key, Acknowledgment: PromotionAcknowledgment}
}

func stringsOf(value string, count int) string {
	result := ""
	for range count {
		result += value
	}
	return result
}

type staticComparisonStore struct{ report ComparisonReport }

func (store staticComparisonStore) Load() (ComparisonReport, error) { return store.report, nil }
func (staticComparisonStore) Save(ComparisonReport) error           { return nil }

type staticPromotionAuthorizer struct{ allowed bool }

func (authorizer staticPromotionAuthorizer) CanReview(context.Context, string) (bool, error) {
	return authorizer.allowed, nil
}

type memoryPromotionRepository struct {
	rows []model.HarnessPromotionAttempt
}

func (repository *memoryPromotionRepository) AppendAttempt(_ context.Context, attempt model.HarnessPromotionAttempt) (bool, model.HarnessPromotionAttempt, error) {
	for _, row := range repository.rows {
		if row.IdempotencyKeyHash == attempt.IdempotencyKeyHash {
			if row.AttemptSHA256 != attempt.AttemptSHA256 {
				return false, model.HarnessPromotionAttempt{}, ErrPromotionIdempotency
			}
			return false, row, nil
		}
	}
	repository.rows = append(repository.rows, attempt)
	return true, attempt, nil
}

func (repository *memoryPromotionRepository) AuditAttempts(_ context.Context, limit int) (int64, int64, int64, int64, []model.HarnessPromotionAttempt, error) {
	rows := append([]model.HarnessPromotionAttempt(nil), repository.rows...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	var recorded, blocked, rejected int64
	for _, row := range rows {
		if row.Outcome == PromotionOutcomeRecorded {
			recorded++
		}
		if row.Outcome == PromotionOutcomeBlocked {
			blocked++
		}
		if row.RequestedDecision == PromotionDecisionReject && row.Outcome == PromotionOutcomeRecorded {
			rejected++
		}
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return int64(len(repository.rows)), recorded, blocked, rejected, rows, nil
}
