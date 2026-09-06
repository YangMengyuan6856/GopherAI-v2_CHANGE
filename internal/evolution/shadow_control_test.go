package evolution

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"GopherAI/model"
)

func TestShadowControlBlocksCurrentNegativeCandidateAndAuditsIdempotently(t *testing.T) {
	report := comparisonFixture(t)
	repository := newMemoryShadowControlRepository(false)
	service, err := NewShadowControlService(staticComparisonStore{report: report}, repository, staticPromotionAuthorizer{allowed: true}, DeterministicShadowEvaluator{}, func() time.Time { return time.Unix(1000, 0) })
	if err != nil {
		t.Fatal(err)
	}
	command := shadowCommand(report, "shadow-control-negative-0001", 0)
	first, err := service.RequestShadow(context.Background(), "reviewer", command)
	if err != nil || !first.Created || first.Event.Outcome != ControlOutcomeBlocked || first.Event.ReasonCode != "controlled_fixture_not_promotable" || first.Event.PointerChanged || first.Pointer != nil || first.AffectsLiveTraffic {
		t.Fatalf("negative candidate did not fail closed: receipt=%+v err=%v", first, err)
	}
	reused, err := service.RequestShadow(context.Background(), "reviewer", command)
	if err != nil || !reused.Reused || reused.Event.ID != first.Event.ID {
		t.Fatalf("blocked request was not idempotent: receipt=%+v err=%v", reused, err)
	}
	conflict := command
	conflict.ExpectedStateVersion = 1
	if _, err := service.RequestShadow(context.Background(), "reviewer", conflict); !errors.Is(err, ErrShadowControlIdempotent) {
		t.Fatalf("idempotency key accepted a different request: %v", err)
	}
	audit, err := service.Audit(context.Background(), "reviewer")
	if err != nil || audit.EventCount != 1 || audit.AppliedCount != 0 || audit.BlockedCount != 1 || len(audit.ActivePointers) != 0 || audit.AffectsLiveTraffic {
		t.Fatalf("unexpected negative control audit: audit=%+v err=%v", audit, err)
	}
}

func TestShadowControlActivatesOnlyIsolatedPointerAndRollsBackOnce(t *testing.T) {
	report := eligibleShadowReport(t)
	repository := newMemoryShadowControlRepository(true)
	now := time.Unix(1100, 0)
	service, _ := NewShadowControlService(staticComparisonStore{report: report}, repository, staticPromotionAuthorizer{allowed: true}, DeterministicShadowEvaluator{}, func() time.Time { now = now.Add(time.Second); return now })
	activation, err := service.RequestShadow(context.Background(), "reviewer", shadowCommand(report, "shadow-control-eligible-0001", 0))
	if err != nil || activation.Event.Outcome != ControlOutcomeApplied || activation.Event.ReasonCode != "isolated_shadow_activated" || !activation.Event.PointerChanged || activation.Pointer == nil || activation.Pointer.StateVersion != 1 || activation.Pointer.CurrentVersion != report.Candidate.ArtifactVersion || activation.AffectsLiveTraffic {
		t.Fatalf("eligible candidate was not isolated and activated: receipt=%+v err=%v", activation, err)
	}
	reused, err := service.RequestShadow(context.Background(), "reviewer", shadowCommand(report, "shadow-control-eligible-0001", 0))
	if err != nil || !reused.Reused || reused.Event.ID != activation.Event.ID || reused.Pointer == nil || reused.Pointer.StateVersion != 1 {
		t.Fatalf("activation replay was not idempotent: receipt=%+v err=%v", reused, err)
	}
	rollback := ShadowControlCommand{Operation: ControlOperationRollback, ArtifactType: report.Candidate.ArtifactType, ExpectedStateVersion: 1, IdempotencyKey: "shadow-control-rollback-0001", Acknowledgment: ShadowControlAck}
	rolledBack, err := service.Rollback(context.Background(), "reviewer", rollback)
	if err != nil || rolledBack.Event.Outcome != ControlOutcomeApplied || rolledBack.Event.ReasonCode != "rollback_completed" || rolledBack.Pointer == nil || rolledBack.Pointer.StateVersion != 2 || rolledBack.Pointer.CurrentVersion != report.Candidate.ParentVersion || rolledBack.AffectsLiveTraffic {
		t.Fatalf("isolated pointer did not roll back: receipt=%+v err=%v", rolledBack, err)
	}
	second := rollback
	second.ExpectedStateVersion = 2
	second.IdempotencyKey = "shadow-control-rollback-0002"
	blocked, err := service.Rollback(context.Background(), "reviewer", second)
	if err != nil || blocked.Event.Outcome != ControlOutcomeBlocked || blocked.Event.ReasonCode != "rollback_unavailable" || blocked.Event.PointerChanged {
		t.Fatalf("second rollback was not blocked: receipt=%+v err=%v", blocked, err)
	}
	audit, err := service.Audit(context.Background(), "reviewer")
	if err != nil || audit.EventCount != 3 || audit.AppliedCount != 2 || audit.BlockedCount != 1 || len(audit.ActivePointers) != 1 || audit.ActivePointers[0].StateVersion != 2 || audit.ActivePointers[0].CurrentVersion != report.Candidate.ParentVersion {
		t.Fatalf("unexpected isolated pointer audit: audit=%+v err=%v", audit, err)
	}
}

func TestShadowControlRequiresSeparatePermissionAndRecordedApproval(t *testing.T) {
	report := eligibleShadowReport(t)
	command := shadowCommand(report, "shadow-control-permission-0001", 0)
	denied, _ := NewShadowControlService(staticComparisonStore{report: report}, newMemoryShadowControlRepository(true), staticPromotionAuthorizer{}, DeterministicShadowEvaluator{}, time.Now)
	if _, err := denied.RequestShadow(context.Background(), "viewer", command); !errors.Is(err, ErrPromotionPermissionDenied) {
		t.Fatalf("ordinary user controlled shadow pointer: %v", err)
	}
	repository := newMemoryShadowControlRepository(false)
	noApproval, _ := NewShadowControlService(staticComparisonStore{report: report}, repository, staticPromotionAuthorizer{allowed: true}, DeterministicShadowEvaluator{}, time.Now)
	receipt, err := noApproval.RequestShadow(context.Background(), "reviewer", command)
	if err != nil || receipt.Event.ReasonCode != "human_approval_required" || receipt.Event.Outcome != ControlOutcomeBlocked {
		t.Fatalf("missing recorded approval did not block: receipt=%+v err=%v", receipt, err)
	}
	repository.approved = true
	reused, err := noApproval.RequestShadow(context.Background(), "reviewer", command)
	if err != nil || !reused.Reused || reused.Event.Outcome != ControlOutcomeBlocked || reused.Pointer != nil {
		t.Fatalf("old idempotency key changed outcome after approval: receipt=%+v err=%v", reused, err)
	}
	command.IdempotencyKey = "shadow-control-permission-0002"
	activation, err := noApproval.RequestShadow(context.Background(), "reviewer", command)
	if err != nil || activation.Event.Outcome != ControlOutcomeApplied || activation.Pointer == nil {
		t.Fatalf("new request did not observe recorded approval: receipt=%+v err=%v", activation, err)
	}
}

func TestDeterministicShadowAssessmentPublishesTwelveRecomputableChecks(t *testing.T) {
	assessment, err := (DeterministicShadowEvaluator{}).Evaluate(context.Background(), eligibleShadowReport(t))
	if err != nil || !assessment.Passed || assessment.PassedCount != 12 || len(assessment.Checks) != 12 || assessment.AffectsLiveTraffic {
		t.Fatalf("unexpected isolated assessment: assessment=%+v err=%v", assessment, err)
	}
	assessment.Checks[0].Passed = false
	if err := validateShadowAssessment(assessment); err == nil {
		t.Fatal("tampered isolated assessment was accepted")
	}
}

func eligibleShadowReport(t *testing.T) ComparisonReport {
	report := comparisonFixture(t)
	report.Candidate.ProductionCandidate = true
	report.Promotion.Eligible = true
	report.Promotion.Decision = "promoted"
	report.Promotion.EvolutionImproved = true
	report.Promotion.ValidationImproved = true
	report.Promotion.SafetyPassed = true
	report.Promotion.HumanReviewComplete = true
	gap := 0.01
	report.Promotion.GeneralizationGap = &gap
	report.Holdout.State, report.Holdout.Opened, report.Holdout.OpenCount = "opened_once", true, 1
	return report
}

func shadowCommand(report ComparisonReport, key string, expected uint64) ShadowControlCommand {
	return ShadowControlCommand{Operation: ControlOperationShadow, ExperimentVersion: report.ExperimentVersion, ArtifactType: report.Candidate.ArtifactType, CandidateVersion: report.Candidate.ArtifactVersion, CandidateSHA256: report.Candidate.ArtifactSHA256, ReportSHA256: report.ReportSHA256, ExpectedStateVersion: expected, IdempotencyKey: key, Acknowledgment: ShadowControlAck}
}

type memoryShadowControlRepository struct {
	approved bool
	events   []model.HarnessControlEvent
	pointers map[string]ActiveHarnessPointer
}

func newMemoryShadowControlRepository(approved bool) *memoryShadowControlRepository {
	return &memoryShadowControlRepository{approved: approved, pointers: map[string]ActiveHarnessPointer{}}
}

func (repository *memoryShadowControlRepository) HasHumanApproval(context.Context, string, string, string) (bool, error) {
	return repository.approved, nil
}

func (repository *memoryShadowControlRepository) AppendBlocked(_ context.Context, event model.HarnessControlEvent) (bool, model.HarnessControlEvent, error) {
	return repository.append(event)
}

func (repository *memoryShadowControlRepository) ActivateShadow(_ context.Context, draft model.HarnessControlEvent, candidate ControlCandidate) (bool, model.HarnessControlEvent, ActiveHarnessPointer, error) {
	if existing, ok, err := repository.existing(draft); ok || err != nil {
		return false, existing, repository.pointers[draft.ArtifactType], err
	}
	before, found := repository.pointers[draft.ArtifactType]
	if !found {
		before = ActiveHarnessPointer{ArtifactType: candidate.ArtifactType, CurrentVersion: candidate.ParentVersion, CurrentSHA256: candidate.ParentSHA256, StateVersion: 0, LastTransition: "implicit_baseline"}
	}
	next, err := ActivateHarnessPointer(before, candidate, ControlAdmission{OfflineGatePassed: true, HumanApproved: true, ShadowPassed: true, SafetyPassed: true}, draft.ExpectedStateVersion)
	if err != nil {
		return false, model.HarnessControlEvent{}, ActiveHarnessPointer{}, err
	}
	draft.Outcome, draft.ReasonCode, draft.PointerChanged = ControlOutcomeApplied, "isolated_shadow_activated", true
	draft.BeforeVersion, draft.BeforeSHA256, draft.BeforeStateVersion = before.CurrentVersion, before.CurrentSHA256, before.StateVersion
	draft.AfterVersion, draft.AfterSHA256, draft.AfterStateVersion = next.CurrentVersion, next.CurrentSHA256, next.StateVersion
	sealShadowControlEvent(&draft)
	if err := validateShadowControlEvent(draft); err != nil {
		return false, model.HarnessControlEvent{}, ActiveHarnessPointer{}, err
	}
	repository.pointers[draft.ArtifactType] = next
	repository.events = append(repository.events, draft)
	return true, draft, next, nil
}

func (repository *memoryShadowControlRepository) RollbackShadow(_ context.Context, draft model.HarnessControlEvent) (bool, model.HarnessControlEvent, ActiveHarnessPointer, error) {
	if existing, ok, err := repository.existing(draft); ok || err != nil {
		return false, existing, repository.pointers[draft.ArtifactType], err
	}
	before, found := repository.pointers[draft.ArtifactType]
	if !found {
		return false, model.HarnessControlEvent{}, ActiveHarnessPointer{}, ErrRollbackUnavailable
	}
	next, err := RollbackHarnessPointer(before, draft.ExpectedStateVersion)
	if err != nil {
		return false, model.HarnessControlEvent{}, ActiveHarnessPointer{}, err
	}
	draft.Outcome, draft.ReasonCode, draft.PointerChanged = ControlOutcomeApplied, "rollback_completed", true
	draft.BeforeVersion, draft.BeforeSHA256, draft.BeforeStateVersion = before.CurrentVersion, before.CurrentSHA256, before.StateVersion
	draft.AfterVersion, draft.AfterSHA256, draft.AfterStateVersion = next.CurrentVersion, next.CurrentSHA256, next.StateVersion
	sealShadowControlEvent(&draft)
	if err := validateShadowControlEvent(draft); err != nil {
		return false, model.HarnessControlEvent{}, ActiveHarnessPointer{}, err
	}
	repository.pointers[draft.ArtifactType] = next
	repository.events = append(repository.events, draft)
	return true, draft, next, nil
}

func (repository *memoryShadowControlRepository) AuditControl(_ context.Context, limit int) (int64, int64, int64, []model.HarnessActivePointer, []model.HarnessControlEvent, error) {
	rows := append([]model.HarnessControlEvent(nil), repository.events...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	var applied, blocked int64
	for _, event := range repository.events {
		if event.Outcome == ControlOutcomeApplied {
			applied++
		} else if event.Outcome == ControlOutcomeBlocked {
			blocked++
		}
	}
	pointers := make([]model.HarnessActivePointer, 0, len(repository.pointers))
	for kind, pointer := range repository.pointers {
		pointers = append(pointers, model.HarnessActivePointer{ID: pointerIdentity(ShadowPointerScope, kind), Scope: ShadowPointerScope, ArtifactType: kind, CurrentVersion: pointer.CurrentVersion, CurrentSHA256: pointer.CurrentSHA256, PreviousVersion: pointer.PreviousVersion, PreviousSHA256: pointer.PreviousSHA256, StateVersion: pointer.StateVersion, LastTransition: pointer.LastTransition})
	}
	return int64(len(repository.events)), applied, blocked, pointers, rows, nil
}

func (repository *memoryShadowControlRepository) append(event model.HarnessControlEvent) (bool, model.HarnessControlEvent, error) {
	if existing, ok, err := repository.existing(event); ok || err != nil {
		return false, existing, err
	}
	if err := validateShadowControlEvent(event); err != nil {
		return false, model.HarnessControlEvent{}, err
	}
	repository.events = append(repository.events, event)
	return true, event, nil
}

func (repository *memoryShadowControlRepository) existing(draft model.HarnessControlEvent) (model.HarnessControlEvent, bool, error) {
	for _, event := range repository.events {
		if event.IdempotencyKeyHash == draft.IdempotencyKeyHash {
			if event.RequestSHA256 != draft.RequestSHA256 {
				return model.HarnessControlEvent{}, false, ErrShadowControlIdempotent
			}
			return event, true, nil
		}
	}
	return model.HarnessControlEvent{}, false, nil
}
