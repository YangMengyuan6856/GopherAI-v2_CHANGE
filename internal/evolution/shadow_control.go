package evolution

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"GopherAI/internal/harness"
	"GopherAI/model"
)

const (
	ShadowControlSchemaVersion = "harness-shadow-control-v1"
	ShadowControlMode          = "governed_isolated_shadow"
	ShadowPointerScope         = "isolated_shadow"
	ShadowControlAck           = "I_CONFIRM_HARNESS_CONTROL_OPERATION"
	ShadowEvaluatorVersion     = "isolated-contract-probe-v1"

	ControlOperationShadow   = "shadow_admission"
	ControlOperationRollback = "rollback"
	ControlOutcomeApplied    = "applied"
	ControlOutcomeBlocked    = "blocked"
)

var (
	ErrShadowControlInvalid    = errors.New("harness shadow control request is invalid")
	ErrShadowControlStale      = errors.New("harness shadow control report binding is stale")
	ErrShadowControlIdempotent = errors.New("harness shadow control idempotency conflict")
)

type ShadowControlRepository interface {
	HasHumanApproval(context.Context, string, string, string) (bool, error)
	AppendBlocked(context.Context, model.HarnessControlEvent) (bool, model.HarnessControlEvent, error)
	ActivateShadow(context.Context, model.HarnessControlEvent, ControlCandidate) (bool, model.HarnessControlEvent, ActiveHarnessPointer, error)
	RollbackShadow(context.Context, model.HarnessControlEvent) (bool, model.HarnessControlEvent, ActiveHarnessPointer, error)
	AuditControl(context.Context, int) (int64, int64, int64, []model.HarnessActivePointer, []model.HarnessControlEvent, error)
}

type ShadowEvaluator interface {
	Evaluate(context.Context, ComparisonReport) (ShadowAssessment, error)
}

type ShadowControlCommand struct {
	Operation            string
	ExperimentVersion    string
	ArtifactType         string
	CandidateVersion     string
	CandidateSHA256      string
	ReportSHA256         string
	ExpectedStateVersion uint64
	IdempotencyKey       string
	Acknowledgment       string
}

type ShadowAssessment struct {
	SchemaVersion      string        `json:"schema_version"`
	Evaluator          string        `json:"evaluator"`
	Scope              string        `json:"scope"`
	CaseCount          int           `json:"case_count"`
	PassedCount        int           `json:"passed_count"`
	SafetyViolations   int           `json:"safety_violations"`
	Passed             bool          `json:"passed"`
	AffectsLiveTraffic bool          `json:"affects_live_traffic"`
	Checks             []ShadowCheck `json:"checks"`
	ReportSHA256       string        `json:"report_sha256"`
}

type ShadowCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

type ShadowPointerView struct {
	ArtifactType    string `json:"artifact_type"`
	CurrentVersion  string `json:"current_version"`
	CurrentSHA256   string `json:"current_sha256"`
	PreviousVersion string `json:"previous_version,omitempty"`
	PreviousSHA256  string `json:"previous_sha256,omitempty"`
	StateVersion    uint64 `json:"state_version"`
	LastTransition  string `json:"last_transition"`
}

type ShadowControlEventView struct {
	ID                   string    `json:"id"`
	Operation            string    `json:"operation"`
	ArtifactType         string    `json:"artifact_type"`
	ExperimentVersion    string    `json:"experiment_version,omitempty"`
	CandidateVersion     string    `json:"candidate_version,omitempty"`
	CandidateSHA256      string    `json:"candidate_sha256,omitempty"`
	ReportSHA256         string    `json:"report_sha256,omitempty"`
	ExpectedStateVersion uint64    `json:"expected_state_version"`
	Outcome              string    `json:"outcome"`
	ReasonCode           string    `json:"reason_code"`
	PointerChanged       bool      `json:"pointer_changed"`
	BeforeVersion        string    `json:"before_version,omitempty"`
	BeforeStateVersion   uint64    `json:"before_state_version"`
	AfterVersion         string    `json:"after_version,omitempty"`
	AfterStateVersion    uint64    `json:"after_state_version"`
	ShadowReportSHA256   string    `json:"shadow_report_sha256,omitempty"`
	RequestSHA256        string    `json:"request_sha256"`
	EventSHA256          string    `json:"event_sha256"`
	CreatedAt            time.Time `json:"created_at"`
}

type ShadowControlAudit struct {
	SchemaVersion      string                   `json:"schema_version"`
	Mode               string                   `json:"mode"`
	Scope              string                   `json:"scope"`
	CanControl         bool                     `json:"can_control"`
	EventCount         int64                    `json:"event_count"`
	AppliedCount       int64                    `json:"applied_count"`
	BlockedCount       int64                    `json:"blocked_count"`
	ActivePointers     []ShadowPointerView      `json:"active_pointers"`
	Latest             []ShadowControlEventView `json:"latest"`
	AffectsLiveTraffic bool                     `json:"affects_live_traffic"`
	Guardrails         []string                 `json:"guardrails"`
	Limitations        []string                 `json:"limitations"`
}

type ShadowControlReceipt struct {
	SchemaVersion      string                 `json:"schema_version"`
	Created            bool                   `json:"created"`
	Reused             bool                   `json:"reused"`
	Event              ShadowControlEventView `json:"event"`
	Pointer            *ShadowPointerView     `json:"pointer,omitempty"`
	AffectsLiveTraffic bool                   `json:"affects_live_traffic"`
}

type ShadowControlService struct {
	reports    ComparisonStore
	repository ShadowControlRepository
	authorizer PromotionAuthorizer
	evaluator  ShadowEvaluator
	clock      func() time.Time
}

func NewShadowControlService(reports ComparisonStore, repository ShadowControlRepository, authorizer PromotionAuthorizer, evaluator ShadowEvaluator, clock func() time.Time) (*ShadowControlService, error) {
	if reports == nil || repository == nil || authorizer == nil || evaluator == nil {
		return nil, errors.New("harness shadow control dependencies are required")
	}
	if clock == nil {
		clock = time.Now
	}
	return &ShadowControlService{reports: reports, repository: repository, authorizer: authorizer, evaluator: evaluator, clock: clock}, nil
}

func (service *ShadowControlService) Audit(ctx context.Context, principal string) (ShadowControlAudit, error) {
	if service == nil || service.repository == nil || service.authorizer == nil || strings.TrimSpace(principal) == "" {
		return ShadowControlAudit{}, ErrPromotionPermissionDenied
	}
	canControl, err := service.authorizer.CanReview(ctx, strings.TrimSpace(principal))
	if err != nil {
		return ShadowControlAudit{}, err
	}
	events, applied, blocked, pointers, rows, err := service.repository.AuditControl(ctx, 20)
	if err != nil {
		return ShadowControlAudit{}, err
	}
	return ShadowControlAudit{
		SchemaVersion: ShadowControlSchemaVersion, Mode: ShadowControlMode, Scope: ShadowPointerScope, CanControl: canControl,
		EventCount: events, AppliedCount: applied, BlockedCount: blocked, ActivePointers: shadowPointerViews(pointers), Latest: shadowControlEventViews(rows), AffectsLiveTraffic: false,
		Guardrails:  []string{"isolated_shadow_scope", "separate_reviewer_permission", "offline_and_holdout_gate", "recorded_human_approval", "independent_shadow_probe", "parent_hash_binding", "optimistic_cas", "append_only_audit", "single_step_rollback"},
		Limitations: []string{"隔离 Shadow 指针不接管生产聊天路由；本控制面不会自动扩大流量或修改 routing-policy。", "当前受控 Fixture 为负收益且不可晋级，请求 Shadow 时应形成 blocked 审计，不能为展示效果伪造活动指针。"},
	}, nil
}

func (service *ShadowControlService) RequestShadow(ctx context.Context, principal string, command ShadowControlCommand) (ShadowControlReceipt, error) {
	if service == nil || service.reports == nil || service.repository == nil || service.authorizer == nil || service.evaluator == nil || !validShadowCommand(command, ControlOperationShadow) {
		return ShadowControlReceipt{}, ErrShadowControlInvalid
	}
	actorHash, err := service.authorize(ctx, principal)
	if err != nil {
		return ShadowControlReceipt{}, err
	}
	report, err := service.reports.Load()
	if err != nil {
		return ShadowControlReceipt{}, err
	}
	if report.ExperimentVersion != command.ExperimentVersion || report.Candidate.ArtifactType != command.ArtifactType || report.Candidate.ArtifactVersion != command.CandidateVersion || report.Candidate.ArtifactSHA256 != command.CandidateSHA256 || report.ReportSHA256 != command.ReportSHA256 {
		return ShadowControlReceipt{}, ErrShadowControlStale
	}
	draft := newShadowControlEvent(command, actorHash, service.clock().UTC())
	approved, err := service.repository.HasHumanApproval(ctx, report.ExperimentVersion, report.Candidate.ArtifactSHA256, report.ReportSHA256)
	if err != nil {
		return ShadowControlReceipt{}, err
	}
	if reason := shadowAdmissionBlockReason(report, approved); reason != "" {
		return service.recordBlocked(ctx, draft, reason)
	}
	assessment, err := service.evaluator.Evaluate(ctx, report)
	if err != nil {
		return ShadowControlReceipt{}, err
	}
	if err := validateShadowAssessment(assessment); err != nil || !assessment.Passed {
		return service.recordBlocked(ctx, draft, "isolated_shadow_failed")
	}
	draft.ShadowReportSHA256 = assessment.ReportSHA256
	candidate := ControlCandidate{ArtifactType: report.Candidate.ArtifactType, ArtifactVersion: report.Candidate.ArtifactVersion, ArtifactSHA256: report.Candidate.ArtifactSHA256, ParentVersion: report.Candidate.ParentVersion, ParentSHA256: digestString(report.Candidate.ParentVersion)}
	created, event, pointer, err := service.repository.ActivateShadow(ctx, draft, candidate)
	if errors.Is(err, ErrPointerStateConflict) || errors.Is(err, ErrParentVersionMismatch) {
		return service.recordBlocked(ctx, draft, "pointer_cas_conflict")
	}
	if err != nil {
		return ShadowControlReceipt{}, err
	}
	if !created && event.Outcome == ControlOutcomeBlocked {
		return shadowControlReceipt(false, event, nil), nil
	}
	return shadowControlReceipt(created, event, &pointer), nil
}

func (service *ShadowControlService) Rollback(ctx context.Context, principal string, command ShadowControlCommand) (ShadowControlReceipt, error) {
	if service == nil || service.repository == nil || service.authorizer == nil || !validShadowCommand(command, ControlOperationRollback) {
		return ShadowControlReceipt{}, ErrShadowControlInvalid
	}
	actorHash, err := service.authorize(ctx, principal)
	if err != nil {
		return ShadowControlReceipt{}, err
	}
	draft := newShadowControlEvent(command, actorHash, service.clock().UTC())
	created, event, pointer, err := service.repository.RollbackShadow(ctx, draft)
	if errors.Is(err, ErrRollbackUnavailable) {
		return service.recordBlocked(ctx, draft, "rollback_unavailable")
	}
	if errors.Is(err, ErrPointerStateConflict) {
		return service.recordBlocked(ctx, draft, "pointer_cas_conflict")
	}
	if err != nil {
		return ShadowControlReceipt{}, err
	}
	if !created && event.Outcome == ControlOutcomeBlocked {
		return shadowControlReceipt(false, event, nil), nil
	}
	return shadowControlReceipt(created, event, &pointer), nil
}

func (service *ShadowControlService) authorize(ctx context.Context, principal string) (string, error) {
	principal = strings.TrimSpace(principal)
	if principal == "" {
		return "", ErrPromotionPermissionDenied
	}
	allowed, err := service.authorizer.CanReview(ctx, principal)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", ErrPromotionPermissionDenied
	}
	return harness.PrincipalHash(principal), nil
}

func (service *ShadowControlService) recordBlocked(ctx context.Context, draft model.HarnessControlEvent, reason string) (ShadowControlReceipt, error) {
	draft.Outcome, draft.ReasonCode, draft.PointerChanged = ControlOutcomeBlocked, reason, false
	sealShadowControlEvent(&draft)
	created, event, err := service.repository.AppendBlocked(ctx, draft)
	if err != nil {
		return ShadowControlReceipt{}, err
	}
	return shadowControlReceipt(created, event, nil), nil
}

type DeterministicShadowEvaluator struct{}

func (DeterministicShadowEvaluator) Evaluate(ctx context.Context, report ComparisonReport) (ShadowAssessment, error) {
	if err := ctx.Err(); err != nil {
		return ShadowAssessment{}, err
	}
	_, allowedPatch := allowedPatchPaths[report.Candidate.ArtifactType][report.Candidate.Patch.Path]
	checks := []ShadowCheck{
		{Name: "production_candidate", Passed: report.Candidate.ProductionCandidate},
		{Name: "static_validation", Passed: report.Candidate.StaticValidationPassed},
		{Name: "human_approval_required", Passed: report.Candidate.RequiresHumanApproval},
		{Name: "promotion_eligible", Passed: report.Promotion.Eligible},
		{Name: "evolution_improved", Passed: report.Promotion.EvolutionImproved},
		{Name: "validation_improved", Passed: report.Promotion.ValidationImproved},
		{Name: "safety_passed", Passed: report.Promotion.SafetyPassed},
		{Name: "human_review_complete", Passed: report.Promotion.HumanReviewComplete},
		{Name: "holdout_opened", Passed: report.Holdout.Opened},
		{Name: "holdout_opened_once", Passed: report.Holdout.OpenCount == 1},
		{Name: "generalization_gap_measured", Passed: report.Promotion.GeneralizationGap != nil},
		{Name: "patch_boundary_allowed", Passed: report.Candidate.Patch.Operation == "replace" && report.Candidate.Patch.VariableCount == 1 && allowedPatch},
	}
	assessment := ShadowAssessment{SchemaVersion: ShadowControlSchemaVersion, Evaluator: ShadowEvaluatorVersion, Scope: ShadowPointerScope, CaseCount: len(checks), SafetyViolations: 0, AffectsLiveTraffic: false, Checks: checks}
	for _, check := range checks {
		if check.Passed {
			assessment.PassedCount++
		}
	}
	assessment.Passed = assessment.PassedCount == assessment.CaseCount && assessment.SafetyViolations == 0
	assessment.ReportSHA256 = shadowAssessmentHash(assessment)
	return assessment, validateShadowAssessment(assessment)
}

func shadowAdmissionBlockReason(report ComparisonReport, approved bool) string {
	if !report.Candidate.ProductionCandidate {
		return "controlled_fixture_not_promotable"
	}
	if !report.Promotion.Eligible || !report.Promotion.EvolutionImproved || !report.Promotion.ValidationImproved {
		return "offline_promotion_gate_failed"
	}
	if !report.Promotion.SafetyPassed {
		return "safety_regression"
	}
	if !report.Holdout.Opened || report.Holdout.OpenCount != 1 || report.Promotion.GeneralizationGap == nil {
		return "sealed_holdout_not_passed"
	}
	if !approved {
		return "human_approval_required"
	}
	return ""
}

func validShadowCommand(command ShadowControlCommand, operation string) bool {
	if command.Operation != operation || command.Acknowledgment != ShadowControlAck || !idempotencyKeyPattern.MatchString(command.IdempotencyKey) || strings.TrimSpace(command.ArtifactType) == "" || command.ArtifactType != "prompt_template" && command.ArtifactType != "context_policy" && command.ArtifactType != "diagnostic_playbook" {
		return false
	}
	if operation == ControlOperationRollback {
		return command.ExperimentVersion == "" && command.CandidateVersion == "" && command.CandidateSHA256 == "" && command.ReportSHA256 == ""
	}
	return len(command.ExperimentVersion) >= 6 && len(command.ExperimentVersion) <= 64 && command.CandidateVersion != "" && len(command.CandidateSHA256) == 64 && len(command.ReportSHA256) == 64
}

func newShadowControlEvent(command ShadowControlCommand, actorHash string, now time.Time) model.HarnessControlEvent {
	idempotencyHash := digestPromotion(actorHash + "\x00" + command.IdempotencyKey)
	requestHash := digestPromotion(strings.Join([]string{command.Operation, ShadowPointerScope, command.ExperimentVersion, command.ArtifactType, command.CandidateVersion, command.CandidateSHA256, command.ReportSHA256, formatStateVersion(command.ExpectedStateVersion), actorHash, idempotencyHash}, "\x00"))
	return model.HarnessControlEvent{SchemaVersion: ShadowControlSchemaVersion, Operation: command.Operation, Scope: ShadowPointerScope, ArtifactType: command.ArtifactType, ExperimentVersion: command.ExperimentVersion, CandidateVersion: command.CandidateVersion, CandidateSHA256: command.CandidateSHA256, ReportSHA256: command.ReportSHA256, ExpectedStateVersion: command.ExpectedStateVersion, ActorHash: actorHash, IdempotencyKeyHash: idempotencyHash, RequestSHA256: requestHash, CreatedAt: now}
}

func sealShadowControlEvent(event *model.HarnessControlEvent) {
	event.ID, event.EventSHA256 = "", ""
	event.EventSHA256 = shadowControlEventHash(*event)
	event.ID = event.EventSHA256
}

func validateShadowControlEvent(event model.HarnessControlEvent) error {
	if event.SchemaVersion != ShadowControlSchemaVersion || event.Scope != ShadowPointerScope || len(event.ID) != 64 || event.ID != event.EventSHA256 || len(event.ActorHash) != 64 || len(event.IdempotencyKeyHash) != 64 || len(event.RequestSHA256) != 64 || event.CreatedAt.IsZero() || strings.TrimSpace(event.ArtifactType) == "" || event.EventSHA256 != shadowControlEventHash(event) {
		return errors.New("harness control event envelope is invalid")
	}
	if event.Operation != ControlOperationShadow && event.Operation != ControlOperationRollback {
		return errors.New("harness control event operation is invalid")
	}
	if event.Outcome == ControlOutcomeBlocked {
		if event.PointerChanged || strings.TrimSpace(event.ReasonCode) == "" || event.AfterStateVersion != 0 {
			return errors.New("blocked harness control event is inconsistent")
		}
		return nil
	}
	if event.Outcome != ControlOutcomeApplied || !event.PointerChanged || event.AfterStateVersion != event.BeforeStateVersion+1 || len(event.AfterSHA256) != 64 {
		return errors.New("applied harness control event is inconsistent")
	}
	if event.Operation == ControlOperationShadow && (event.ReasonCode != "isolated_shadow_activated" || len(event.ShadowReportSHA256) != 64 || len(event.CandidateSHA256) != 64 || event.AfterVersion != event.CandidateVersion || event.AfterSHA256 != event.CandidateSHA256) {
		return errors.New("shadow activation event is inconsistent")
	}
	if event.Operation == ControlOperationRollback && (event.ReasonCode != "rollback_completed" || event.ShadowReportSHA256 != "") {
		return errors.New("shadow rollback event is inconsistent")
	}
	return nil
}

func shadowControlEventHash(event model.HarnessControlEvent) string {
	event.ID, event.EventSHA256, event.CreatedAt = "", "", time.Time{}
	encoded, _ := json.Marshal(event)
	return digestBytes(encoded)
}

func validateShadowAssessment(assessment ShadowAssessment) error {
	expectedChecks := []string{"production_candidate", "static_validation", "human_approval_required", "promotion_eligible", "evolution_improved", "validation_improved", "safety_passed", "human_review_complete", "holdout_opened", "holdout_opened_once", "generalization_gap_measured", "patch_boundary_allowed"}
	if assessment.SchemaVersion != ShadowControlSchemaVersion || assessment.Evaluator != ShadowEvaluatorVersion || assessment.Scope != ShadowPointerScope || assessment.CaseCount != len(expectedChecks) || len(assessment.Checks) != assessment.CaseCount || assessment.Passed != (assessment.PassedCount == assessment.CaseCount && assessment.SafetyViolations == 0) || assessment.AffectsLiveTraffic || len(assessment.ReportSHA256) != 64 || shadowAssessmentHash(assessment) != assessment.ReportSHA256 {
		return errors.New("isolated shadow assessment is invalid")
	}
	passed := 0
	for index, check := range assessment.Checks {
		if check.Name != expectedChecks[index] {
			return errors.New("isolated shadow assessment order is invalid")
		}
		if check.Passed {
			passed++
		}
	}
	if passed != assessment.PassedCount {
		return errors.New("isolated shadow assessment count is invalid")
	}
	return nil
}

func shadowAssessmentHash(assessment ShadowAssessment) string {
	assessment.ReportSHA256 = ""
	encoded, _ := json.Marshal(assessment)
	return digestBytes(encoded)
}

func shadowControlReceipt(created bool, event model.HarnessControlEvent, pointer *ActiveHarnessPointer) ShadowControlReceipt {
	receipt := ShadowControlReceipt{SchemaVersion: ShadowControlSchemaVersion, Created: created, Reused: !created, Event: shadowControlEventView(event), AffectsLiveTraffic: false}
	if pointer != nil {
		view := shadowPointerView(*pointer)
		receipt.Pointer = &view
	}
	return receipt
}

func shadowControlEventViews(rows []model.HarnessControlEvent) []ShadowControlEventView {
	views := make([]ShadowControlEventView, 0, len(rows))
	for _, row := range rows {
		views = append(views, shadowControlEventView(row))
	}
	return views
}

func shadowControlEventView(row model.HarnessControlEvent) ShadowControlEventView {
	return ShadowControlEventView{ID: row.ID, Operation: row.Operation, ArtifactType: row.ArtifactType, ExperimentVersion: row.ExperimentVersion, CandidateVersion: row.CandidateVersion, CandidateSHA256: row.CandidateSHA256, ReportSHA256: row.ReportSHA256, ExpectedStateVersion: row.ExpectedStateVersion, Outcome: row.Outcome, ReasonCode: row.ReasonCode, PointerChanged: row.PointerChanged, BeforeVersion: row.BeforeVersion, BeforeStateVersion: row.BeforeStateVersion, AfterVersion: row.AfterVersion, AfterStateVersion: row.AfterStateVersion, ShadowReportSHA256: row.ShadowReportSHA256, RequestSHA256: row.RequestSHA256, EventSHA256: row.EventSHA256, CreatedAt: row.CreatedAt.UTC()}
}

func shadowPointerViews(rows []model.HarnessActivePointer) []ShadowPointerView {
	views := make([]ShadowPointerView, 0, len(rows))
	for _, row := range rows {
		views = append(views, shadowPointerView(activePointerFromModel(row)))
	}
	return views
}

func shadowPointerView(pointer ActiveHarnessPointer) ShadowPointerView {
	return ShadowPointerView{ArtifactType: pointer.ArtifactType, CurrentVersion: pointer.CurrentVersion, CurrentSHA256: pointer.CurrentSHA256, PreviousVersion: pointer.PreviousVersion, PreviousSHA256: pointer.PreviousSHA256, StateVersion: pointer.StateVersion, LastTransition: pointer.LastTransition}
}

func activePointerFromModel(row model.HarnessActivePointer) ActiveHarnessPointer {
	return ActiveHarnessPointer{ArtifactType: row.ArtifactType, CurrentVersion: row.CurrentVersion, CurrentSHA256: row.CurrentSHA256, PreviousVersion: row.PreviousVersion, PreviousSHA256: row.PreviousSHA256, StateVersion: row.StateVersion, LastTransition: row.LastTransition}
}

func formatStateVersion(value uint64) string {
	return strconv.FormatUint(value, 10)
}
