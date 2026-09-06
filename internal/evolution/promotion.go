package evolution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"GopherAI/internal/harness"
	"GopherAI/model"
)

const (
	PromotionSchemaVersion  = "harness-promotion-review-v1"
	PromotionMode           = "human_gate_no_activation"
	PromotionReviewerRole   = "harness_reviewer"
	PromotionAcknowledgment = "I_CONFIRM_HUMAN_PROMOTION_REVIEW"

	PromotionDecisionApprove = "approved"
	PromotionDecisionReject  = "rejected"
	PromotionOutcomeRecorded = "recorded"
	PromotionOutcomeBlocked  = "blocked"
)

var (
	ErrPromotionPermissionDenied = errors.New("harness promotion reviewer permission is required")
	ErrPromotionRequestInvalid   = errors.New("harness promotion request is invalid")
	ErrPromotionReportStale      = errors.New("harness promotion report binding is stale")
	ErrPromotionIdempotency      = errors.New("harness promotion idempotency conflict")
	idempotencyKeyPattern        = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)
)

type PromotionAuthorizer interface {
	CanReview(context.Context, string) (bool, error)
}

type PromotionRepository interface {
	AppendAttempt(context.Context, model.HarnessPromotionAttempt) (bool, model.HarnessPromotionAttempt, error)
	AuditAttempts(context.Context, int) (int64, int64, int64, int64, []model.HarnessPromotionAttempt, error)
}

type PromotionReviewCommand struct {
	ExperimentVersion string
	CandidateSHA256   string
	ReportSHA256      string
	Decision          string
	ReasonCode        string
	IdempotencyKey    string
	Acknowledgment    string
}

type PromotionAttemptView struct {
	ID                   string    `json:"id"`
	ExperimentVersion    string    `json:"experiment_version"`
	CandidateSHA256      string    `json:"candidate_sha256"`
	ReportSHA256         string    `json:"report_sha256"`
	RequestedDecision    string    `json:"requested_decision"`
	Outcome              string    `json:"outcome"`
	ReasonCode           string    `json:"reason_code"`
	BaseVersion          string    `json:"base_version"`
	ActivePointerChanged bool      `json:"active_pointer_changed"`
	AttemptSHA256        string    `json:"attempt_sha256"`
	CreatedAt            time.Time `json:"created_at"`
}

type PromotionAudit struct {
	SchemaVersion  string                 `json:"schema_version"`
	Mode           string                 `json:"mode"`
	CanReview      bool                   `json:"can_review"`
	AttemptCount   int64                  `json:"attempt_count"`
	RecordedCount  int64                  `json:"recorded_count"`
	BlockedCount   int64                  `json:"blocked_count"`
	RejectedCount  int64                  `json:"rejected_count"`
	ActivePointers int                    `json:"active_pointers"`
	Latest         []PromotionAttemptView `json:"latest"`
	Guardrails     []string               `json:"guardrails"`
	Limitations    []string               `json:"limitations"`
}

type PromotionReviewReceipt struct {
	SchemaVersion      string               `json:"schema_version"`
	Created            bool                 `json:"created"`
	Reused             bool                 `json:"reused"`
	Attempt            PromotionAttemptView `json:"attempt"`
	ActivePointers     int                  `json:"active_pointers"`
	AffectsLiveTraffic bool                 `json:"affects_live_traffic"`
}

type PromotionService struct {
	reports    ComparisonStore
	repository PromotionRepository
	authorizer PromotionAuthorizer
	clock      func() time.Time
}

func NewPromotionService(reports ComparisonStore, repository PromotionRepository, authorizer PromotionAuthorizer, clock func() time.Time) (*PromotionService, error) {
	if reports == nil || repository == nil || authorizer == nil {
		return nil, errors.New("harness promotion dependencies are required")
	}
	if clock == nil {
		clock = time.Now
	}
	return &PromotionService{reports: reports, repository: repository, authorizer: authorizer, clock: clock}, nil
}

func (service *PromotionService) Audit(ctx context.Context, principal string) (PromotionAudit, error) {
	if service == nil || service.repository == nil || service.authorizer == nil || strings.TrimSpace(principal) == "" {
		return PromotionAudit{}, ErrPromotionPermissionDenied
	}
	canReview, err := service.authorizer.CanReview(ctx, strings.TrimSpace(principal))
	if err != nil {
		return PromotionAudit{}, err
	}
	attempts, recorded, blocked, rejected, rows, err := service.repository.AuditAttempts(ctx, 20)
	if err != nil {
		return PromotionAudit{}, err
	}
	return PromotionAudit{
		SchemaVersion: PromotionSchemaVersion, Mode: PromotionMode, CanReview: canReview,
		AttemptCount: attempts, RecordedCount: recorded, BlockedCount: blocked, RejectedCount: rejected,
		ActivePointers: 0, Latest: promotionAttemptViews(rows),
		Guardrails:  []string{"separate_reviewer_permission", "report_hash_binding", "append_only_decisions", "idempotency_conflict_detection", "blocked_attempts_are_audited", "no_active_pointer_write"},
		Limitations: []string{"当前纵切只记录人工拒绝或批准门禁结果，不启动 Shadow、不创建活动指针，也不影响线上聊天。", "当前受控候选没有证明收益且不是 Production Candidate，因此批准请求必须被稳定阻断。"},
	}, nil
}

func (service *PromotionService) Review(ctx context.Context, principal string, command PromotionReviewCommand) (PromotionReviewReceipt, error) {
	if service == nil || service.reports == nil || service.repository == nil || service.authorizer == nil {
		return PromotionReviewReceipt{}, ErrPromotionRequestInvalid
	}
	principal = strings.TrimSpace(principal)
	if principal == "" || !validPromotionCommand(command) {
		return PromotionReviewReceipt{}, ErrPromotionRequestInvalid
	}
	allowed, err := service.authorizer.CanReview(ctx, principal)
	if err != nil {
		return PromotionReviewReceipt{}, err
	}
	if !allowed {
		return PromotionReviewReceipt{}, ErrPromotionPermissionDenied
	}
	report, err := service.reports.Load()
	if err != nil {
		return PromotionReviewReceipt{}, err
	}
	if report.ExperimentVersion != command.ExperimentVersion || report.Candidate.ArtifactSHA256 != command.CandidateSHA256 || report.ReportSHA256 != command.ReportSHA256 {
		return PromotionReviewReceipt{}, ErrPromotionReportStale
	}
	outcome, reason := promotionOutcome(report, command.Decision, command.ReasonCode)
	reviewerHash := harness.PrincipalHash(principal)
	idempotencyHash := digestPromotion(reviewerHash + "\x00" + command.IdempotencyKey)
	now := service.clock().UTC()
	attempt := model.HarnessPromotionAttempt{
		SchemaVersion: PromotionSchemaVersion, ExperimentVersion: report.ExperimentVersion, CandidateSHA256: report.Candidate.ArtifactSHA256,
		ReportSHA256: report.ReportSHA256, RequestedDecision: command.Decision, Outcome: outcome, ReasonCode: reason,
		BaseVersion: report.Candidate.ParentVersion, ReviewerHash: reviewerHash, IdempotencyKeyHash: idempotencyHash,
		ActivePointerChanged: false, CreatedAt: now,
	}
	attempt.AttemptSHA256 = promotionAttemptIdentity(attempt)
	attempt.ID = attempt.AttemptSHA256
	if err := validatePromotionAttempt(attempt); err != nil {
		return PromotionReviewReceipt{}, err
	}
	created, stored, err := service.repository.AppendAttempt(ctx, attempt)
	if err != nil {
		return PromotionReviewReceipt{}, err
	}
	return PromotionReviewReceipt{SchemaVersion: PromotionSchemaVersion, Created: created, Reused: !created, Attempt: promotionAttemptView(stored), ActivePointers: 0, AffectsLiveTraffic: false}, nil
}

func validPromotionCommand(command PromotionReviewCommand) bool {
	if len(command.ExperimentVersion) < 6 || len(command.ExperimentVersion) > 64 || len(command.CandidateSHA256) != 64 || len(command.ReportSHA256) != 64 ||
		!idempotencyKeyPattern.MatchString(command.IdempotencyKey) || command.Acknowledgment != PromotionAcknowledgment {
		return false
	}
	switch command.Decision {
	case PromotionDecisionReject:
		switch command.ReasonCode {
		case "candidate_no_measured_gain", "risk_not_acceptable", "needs_more_evidence":
			return true
		}
	case PromotionDecisionApprove:
		return command.ReasonCode == "human_approval_requested"
	}
	return false
}

func promotionOutcome(report ComparisonReport, decision, requestedReason string) (string, string) {
	if decision == PromotionDecisionReject {
		return PromotionOutcomeRecorded, requestedReason
	}
	if !report.Candidate.ProductionCandidate {
		return PromotionOutcomeBlocked, "controlled_fixture_not_promotable"
	}
	if !report.Promotion.Eligible || !report.Promotion.EvolutionImproved || !report.Promotion.ValidationImproved {
		return PromotionOutcomeBlocked, "offline_promotion_gate_failed"
	}
	if !report.Promotion.HumanReviewComplete {
		return PromotionOutcomeBlocked, "human_labels_pending"
	}
	if !report.Promotion.SafetyPassed {
		return PromotionOutcomeBlocked, "safety_regression"
	}
	if !report.Holdout.Opened || report.Holdout.OpenCount != 1 || report.Promotion.GeneralizationGap == nil {
		return PromotionOutcomeBlocked, "sealed_holdout_not_passed"
	}
	return PromotionOutcomeRecorded, "human_approved_for_shadow"
}

func validatePromotionAttempt(attempt model.HarnessPromotionAttempt) error {
	if attempt.SchemaVersion != PromotionSchemaVersion || len(attempt.ID) != 64 || attempt.ID != attempt.AttemptSHA256 || len(attempt.ExperimentVersion) < 6 ||
		len(attempt.CandidateSHA256) != 64 || len(attempt.ReportSHA256) != 64 || len(attempt.ReviewerHash) != 64 || len(attempt.IdempotencyKeyHash) != 64 ||
		(attempt.RequestedDecision != PromotionDecisionApprove && attempt.RequestedDecision != PromotionDecisionReject) ||
		(attempt.Outcome != PromotionOutcomeRecorded && attempt.Outcome != PromotionOutcomeBlocked) || strings.TrimSpace(attempt.ReasonCode) == "" ||
		strings.TrimSpace(attempt.BaseVersion) == "" || attempt.ActivePointerChanged || attempt.CreatedAt.IsZero() || !validPromotionAttemptSemantics(attempt) || promotionAttemptIdentity(attempt) != attempt.AttemptSHA256 {
		return errors.New("harness promotion attempt is invalid")
	}
	return nil
}

func validPromotionAttemptSemantics(attempt model.HarnessPromotionAttempt) bool {
	if attempt.RequestedDecision == PromotionDecisionReject {
		if attempt.Outcome != PromotionOutcomeRecorded {
			return false
		}
		switch attempt.ReasonCode {
		case "candidate_no_measured_gain", "risk_not_acceptable", "needs_more_evidence":
			return true
		default:
			return false
		}
	}
	if attempt.Outcome == PromotionOutcomeRecorded {
		return attempt.ReasonCode == "human_approved_for_shadow"
	}
	switch attempt.ReasonCode {
	case "controlled_fixture_not_promotable", "offline_promotion_gate_failed", "human_labels_pending", "safety_regression", "sealed_holdout_not_passed":
		return true
	default:
		return false
	}
}

func promotionAttemptIdentity(attempt model.HarnessPromotionAttempt) string {
	return digestPromotion(strings.Join([]string{attempt.SchemaVersion, attempt.ExperimentVersion, attempt.CandidateSHA256, attempt.ReportSHA256, attempt.RequestedDecision,
		attempt.Outcome, attempt.ReasonCode, attempt.BaseVersion, attempt.ReviewerHash, attempt.IdempotencyKeyHash}, "\x00"))
}

func digestPromotion(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func promotionAttemptViews(rows []model.HarnessPromotionAttempt) []PromotionAttemptView {
	views := make([]PromotionAttemptView, 0, len(rows))
	for _, row := range rows {
		views = append(views, promotionAttemptView(row))
	}
	return views
}

func promotionAttemptView(row model.HarnessPromotionAttempt) PromotionAttemptView {
	return PromotionAttemptView{ID: row.ID, ExperimentVersion: row.ExperimentVersion, CandidateSHA256: row.CandidateSHA256, ReportSHA256: row.ReportSHA256,
		RequestedDecision: row.RequestedDecision, Outcome: row.Outcome, ReasonCode: row.ReasonCode, BaseVersion: row.BaseVersion,
		ActivePointerChanged: row.ActivePointerChanged, AttemptSHA256: row.AttemptSHA256, CreatedAt: row.CreatedAt.UTC()}
}
