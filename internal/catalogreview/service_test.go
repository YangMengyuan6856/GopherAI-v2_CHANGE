package catalogreview

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/evalgovernance"
	"GopherAI/model"
)

type artifactStub struct{ snapshot Snapshot }

func (stub artifactStub) Load() (Snapshot, error) { return stub.snapshot, nil }

type memoryRepository struct {
	rows               []model.EvaluationCatalogReview
	truncateStoredTime bool
}

func (repository *memoryRepository) ListLatest(_ context.Context, catalogSHA, governanceSHA, reviewerHash string) ([]model.EvaluationCatalogReview, error) {
	latest := map[string]model.EvaluationCatalogReview{}
	for _, row := range repository.rows {
		if row.CatalogSHA256 == catalogSHA && row.GovernanceSHA256 == governanceSHA && row.ReviewerHash == reviewerHash && row.Revision > latest[row.CaseID].Revision {
			latest[row.CaseID] = row
		}
	}
	result := make([]model.EvaluationCatalogReview, 0, len(latest))
	for _, row := range latest {
		result = append(result, row)
	}
	return result, nil
}

func (repository *memoryRepository) Append(_ context.Context, candidate model.EvaluationCatalogReview) (bool, model.EvaluationCatalogReview, error) {
	if repository.truncateStoredTime {
		stored := candidate.CreatedAt.Truncate(time.Second)
		candidate.CreatedAt = time.Date(stored.Year(), stored.Month(), stored.Day(), stored.Hour(), stored.Minute(), stored.Second(), 0, stored.Location())
	}
	for _, row := range repository.rows {
		if row.IdempotencyKeyHash == candidate.IdempotencyKeyHash {
			if row.RequestSHA256 != candidate.RequestSHA256 {
				return false, model.EvaluationCatalogReview{}, ErrIdempotencyConflict
			}
			return false, row, nil
		}
	}
	current := 0
	for _, row := range repository.rows {
		if row.CatalogSHA256 == candidate.CatalogSHA256 && row.GovernanceSHA256 == candidate.GovernanceSHA256 && row.ReviewerHash == candidate.ReviewerHash && row.CaseID == candidate.CaseID && row.Revision > current {
			current = row.Revision
		}
	}
	if candidate.ExpectedRevision != current || candidate.Revision != current+1 {
		return false, model.EvaluationCatalogReview{}, ErrRevisionConflict
	}
	repository.rows = append(repository.rows, candidate)
	return true, candidate, nil
}

func TestServiceSurvivesMySQLSecondPrecisionRoundTrip(t *testing.T) {
	snapshot := reviewSnapshot()
	repository := &memoryRepository{truncateStoredTime: true}
	service := NewService(artifactStub{snapshot: snapshot}, repository, func() time.Time {
		return time.Date(2026, 9, 8, 0, 43, 31, 987654321, time.UTC)
	})
	receipt, err := service.Submit(context.Background(), "alice", ReviewCommand{
		CatalogSHA256: snapshot.CatalogSHA256, GovernanceSHA256: snapshot.Governance.ManifestSHA256,
		CaseID: snapshot.Cases[0].ID, CaseSHA256: snapshot.Cases[0].CaseSHA256,
		ExpectedRevision: 0, Decision: "approved", ReasonCodes: []string{"label_verified"},
		IdempotencyKey: "catalog-mysql-time-precision-0001", Acknowledgment: Acknowledgment,
	})
	wantReviewedAt := time.Date(2026, 9, 8, 0, 43, 31, 0, time.UTC).In(time.Local).UTC()
	if err != nil || receipt.Progress.Reviewed != 1 || receipt.Progress.Pending != 2 || !receipt.Review.ReviewedAt.Equal(wantReviewedAt) {
		t.Fatalf("second precision round-trip failed: receipt=%+v err=%v", receipt, err)
	}
	next, err := service.List(context.Background(), "alice", Query{Status: "pending", Page: 1, PageSize: 1})
	if err != nil || len(next.Cases) != 1 || next.Cases[0].ID != "case-2" || next.Progress.Reviewed != 1 {
		t.Fatalf("next pending case was unavailable after round-trip: workbench=%+v err=%v", next, err)
	}
}

func TestUpgradeTimezoneShiftedV3ReviewWithoutTouchingValidV3(t *testing.T) {
	snapshot := reviewSnapshot()
	local := time.FixedZone("database-local", 8*60*60)
	originalUTC := time.Date(2026, 9, 8, 2, 54, 33, 0, time.UTC)
	review := model.EvaluationCatalogReview{
		SchemaVersion: PreviousReviewSchemaVersion, DatasetVersion: snapshot.DatasetVersion,
		CatalogSHA256: snapshot.CatalogSHA256, GovernanceSHA256: snapshot.Governance.ManifestSHA256,
		Slice: snapshot.Cases[1].Slice, CaseID: snapshot.Cases[1].ID, CaseSHA256: snapshot.Cases[1].CaseSHA256,
		ReviewerHash: digest("alice"), Revision: 1, Decision: "approved", ReasonCodesJSON: `["label_verified"]`,
		ExpectedRevision: 0, IdempotencyKeyHash: digest("timezone-shifted-idempotency"), CreatedAt: originalUTC,
	}
	review.RequestSHA256 = reviewRequestSHA(review)
	review.ReviewSHA256 = reviewSHA(review)
	review.ID = review.ReviewSHA256
	review.CreatedAt = originalUTC.In(local)
	if validateReview(review) == nil {
		t.Fatal("timezone-shifted v3 review unexpectedly validated before migration")
	}

	upgraded, changed, err := upgradeLegacyReview(snapshot, review)
	if err != nil || !changed || upgraded.SchemaVersion != ReviewSchemaVersion || upgraded.PreviousReviewSHA256 != review.ReviewSHA256 || upgraded.CreatedAt != review.CreatedAt {
		t.Fatalf("timezone-shifted v3 review was not preserved: upgraded=%+v changed=%t err=%v", upgraded, changed, err)
	}
	if err := validateReview(upgraded); err != nil {
		t.Fatalf("upgraded timezone-shifted review did not validate: %v", err)
	}

	validV3 := upgraded
	validV3.SchemaVersion = PreviousReviewSchemaVersion
	validV3.PreviousReviewSHA256 = ""
	validV3.RequestSHA256 = reviewRequestSHA(validV3)
	validV3.ReviewSHA256 = reviewSHA(validV3)
	validV3.ID = validV3.ReviewSHA256
	unchanged, changed, err := upgradeLegacyReview(snapshot, validV3)
	if err != nil || changed || unchanged.ReviewSHA256 != validV3.ReviewSHA256 {
		t.Fatalf("valid v3 review was rewritten: unchanged=%+v changed=%t err=%v", unchanged, changed, err)
	}
}

func TestUpgradeLegacyReviewPreservesSemanticDecisionAndIsIdempotent(t *testing.T) {
	snapshot := reviewSnapshot()
	originalTime := time.Date(2026, 9, 8, 0, 43, 31, 987654321, time.UTC)
	review := model.EvaluationCatalogReview{
		SchemaVersion: LegacyReviewSchemaVersion, DatasetVersion: snapshot.DatasetVersion,
		CatalogSHA256: snapshot.CatalogSHA256, GovernanceSHA256: snapshot.Governance.ManifestSHA256,
		Slice: snapshot.Cases[0].Slice, CaseID: snapshot.Cases[0].ID, CaseSHA256: snapshot.Cases[0].CaseSHA256,
		ReviewerHash: digest("alice"), Revision: 1, Decision: "approved", ReasonCodesJSON: `["label_verified"]`,
		ExpectedRevision: 0, IdempotencyKeyHash: digest("legacy-idempotency"), CreatedAt: originalTime,
	}
	review.RequestSHA256 = reviewRequestSHA(review)
	review.ReviewSHA256 = digest(strings.Join([]string{review.SchemaVersion, review.DatasetVersion, review.CatalogSHA256, review.GovernanceSHA256, review.Slice, review.CaseID, review.CaseSHA256, review.ReviewerHash, "1", review.Decision, review.ReasonCodesJSON, "0", review.IdempotencyKeyHash, review.RequestSHA256, originalTime.Format(time.RFC3339Nano)}, "\x00"))
	review.ID = review.ReviewSHA256
	review.CreatedAt = originalTime.Truncate(time.Second)

	upgraded, changed, err := upgradeLegacyReview(snapshot, review)
	if err != nil || !changed || upgraded.SchemaVersion != ReviewSchemaVersion || upgraded.PreviousReviewSHA256 != review.ReviewSHA256 || upgraded.Decision != review.Decision || upgraded.CreatedAt != review.CreatedAt {
		t.Fatalf("legacy review was not safely upgraded: upgraded=%+v changed=%t err=%v", upgraded, changed, err)
	}
	if err := validateReview(upgraded); err != nil {
		t.Fatalf("upgraded review did not validate: %v", err)
	}
	again, changed, err := upgradeLegacyReview(snapshot, upgraded)
	if err != nil || changed || again.ReviewSHA256 != upgraded.ReviewSHA256 {
		t.Fatalf("legacy migration was not idempotent: again=%+v changed=%t err=%v", again, changed, err)
	}

	tampered := review
	tampered.RequestSHA256 = strings.Repeat("f", 64)
	if _, _, err := upgradeLegacyReview(snapshot, tampered); !errors.Is(err, ErrLegacyReviewMigration) {
		t.Fatalf("tampered legacy review was migrated: %v", err)
	}
}

func TestFileArtifactStoreLoadsValidatedFullCatalog(t *testing.T) {
	snapshot, err := NewFileArtifactStore("../../evals/devsupport-eval-v1.manifest.json").Load()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.DatasetVersion != "devsupport-eval-v1" || len(snapshot.CatalogSHA256) != 64 || len(snapshot.Cases) != 320 {
		t.Fatalf("unexpected full catalog snapshot: version=%s hash=%s cases=%d", snapshot.DatasetVersion, snapshot.CatalogSHA256, len(snapshot.Cases))
	}
	if snapshot.Governance.GovernanceVersion != "devsupport-eval-governance-v2-human-adjudicated" || len(snapshot.Governance.ManifestSHA256) != 64 || len(snapshot.Governance.Slices) != 6 {
		t.Fatalf("catalog governance was not hash-bound: %+v", snapshot.Governance)
	}
	first := snapshot.Cases[0]
	if first.ID != "intent-v1-001" || first.Slice != "intent" || first.Prompt == "" || first.Content["reviewed_by"] != nil || first.Content["dataset_version"] != nil || first.Content["id"] != nil {
		t.Fatalf("catalog case was not normalized safely: %+v", first)
	}
	if first.ReviewGuide.TruthType != "spec_derived" || first.ReviewGuide.ReviewQuestion == "" || len(first.ReviewGuide.SourceReferences) == 0 {
		t.Fatalf("intent review guide is incomplete: %+v", first.ReviewGuide)
	}
	foundGroundedRAG := false
	for _, item := range snapshot.Cases {
		if item.Slice == "rag" && len(item.ReviewGuide.EvidenceExcerpts) > 0 {
			foundGroundedRAG = item.ReviewGuide.TruthType == "fixture_grounded" && item.ReviewGuide.EvidenceExcerpts[0].Content != ""
			break
		}
	}
	if !foundGroundedRAG {
		t.Fatal("no RAG case exposed independently governed fixture evidence")
	}
	foundToolContract := false
	for _, item := range snapshot.Cases {
		if item.Slice == "tool" && len(item.ReviewGuide.EvidenceExcerpts) == 1 {
			foundToolContract = strings.Contains(item.ReviewGuide.EvidenceExcerpts[0].Content, "counter_semantics")
			break
		}
	}
	if !foundToolContract {
		t.Fatal("no tool case exposed its governed schema and execution trajectory")
	}
}

func TestServicePaginatesAndScopesAppendOnlyReviewProgress(t *testing.T) {
	snapshot := reviewSnapshot()
	repository := new(memoryRepository)
	service := NewService(artifactStub{snapshot: snapshot}, repository, func() time.Time { return time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC) })
	initial, err := service.List(context.Background(), "alice", Query{Status: "pending", Page: 1, PageSize: 1})
	if err != nil || initial.Progress.Total != 3 || initial.Progress.Pending != 3 || initial.FilteredTotal != 3 || len(initial.Cases) != 1 || initial.Cases[0].ID != "case-1" {
		t.Fatalf("unexpected initial workbench: %+v err=%v", initial, err)
	}
	command := ReviewCommand{
		CatalogSHA256: snapshot.CatalogSHA256, GovernanceSHA256: snapshot.Governance.ManifestSHA256, CaseID: "case-1", CaseSHA256: snapshot.Cases[0].CaseSHA256,
		ExpectedRevision: 0, Decision: "approved", ReasonCodes: []string{"label_verified"},
		IdempotencyKey: "catalog-review-case-1-v1", Acknowledgment: Acknowledgment,
	}
	first, err := service.Submit(context.Background(), "alice", command)
	if err != nil || !first.Created || first.Review.Revision != 1 || first.Progress.Reviewed != 1 || first.Progress.Approved != 1 || first.Progress.ReadyForMaterializing {
		t.Fatalf("unexpected first review: %+v err=%v", first, err)
	}
	replayed, err := service.Submit(context.Background(), "alice", command)
	if err != nil || replayed.Created || replayed.Review.Revision != 1 || len(repository.rows) != 1 {
		t.Fatalf("idempotent replay failed: %+v err=%v rows=%d", replayed, err, len(repository.rows))
	}
	command.ExpectedRevision = 1
	command.Decision = "rejected"
	command.ReasonCodes = []string{"missing_context", "ambiguous_input"}
	command.IdempotencyKey = "catalog-review-case-1-v2"
	corrected, err := service.Submit(context.Background(), "alice", command)
	if err != nil || !corrected.Created || corrected.Review.Revision != 2 || corrected.Progress.Rejected != 1 || corrected.Progress.Approved != 0 {
		t.Fatalf("append-only correction failed: %+v err=%v", corrected, err)
	}
	oldReplay := ReviewCommand{
		CatalogSHA256: snapshot.CatalogSHA256, GovernanceSHA256: snapshot.Governance.ManifestSHA256, CaseID: "case-1", CaseSHA256: snapshot.Cases[0].CaseSHA256,
		ExpectedRevision: 0, Decision: "approved", ReasonCodes: []string{"label_verified"},
		IdempotencyKey: "catalog-review-case-1-v1", Acknowledgment: Acknowledgment,
	}
	replayedAfterCorrection, err := service.Submit(context.Background(), "alice", oldReplay)
	if err != nil || replayedAfterCorrection.Created || replayedAfterCorrection.Review.Revision != 1 || replayedAfterCorrection.Progress.Rejected != 1 || replayedAfterCorrection.Progress.Approved != 0 {
		t.Fatalf("old replay changed latest progress: %+v err=%v", replayedAfterCorrection, err)
	}
	alice, _ := service.List(context.Background(), "alice", Query{Slice: "intent", Status: "rejected", Page: 1, PageSize: 5})
	bob, _ := service.List(context.Background(), "bob", Query{Status: "pending", Page: 1, PageSize: 5})
	if alice.FilteredTotal != 1 || alice.Cases[0].Review.Revision != 2 || alice.Progress.Reviewed != 1 || bob.Progress.Reviewed != 0 || bob.FilteredTotal != 3 {
		t.Fatalf("reviewer scope or filter failed: alice=%+v bob=%+v", alice, bob)
	}
	command.ExpectedRevision = 2
	command.ReasonCodes = []string{"criterion_not_independent"}
	command.IdempotencyKey = "catalog-review-case-1-v3"
	if receipt, submitErr := service.Submit(context.Background(), "alice", command); submitErr != nil || receipt.Review.Revision != 3 {
		t.Fatalf("governance-specific rejection was not accepted: %+v err=%v", receipt, submitErr)
	}
}

func TestServiceRejectsStaleUnsafeAndConflictingReviews(t *testing.T) {
	snapshot := reviewSnapshot()
	repository := new(memoryRepository)
	service := NewService(artifactStub{snapshot: snapshot}, repository, time.Now)
	valid := ReviewCommand{
		CatalogSHA256: snapshot.CatalogSHA256, GovernanceSHA256: snapshot.Governance.ManifestSHA256, CaseID: "case-1", CaseSHA256: snapshot.Cases[0].CaseSHA256,
		ExpectedRevision: 0, Decision: "approved", ReasonCodes: []string{"label_verified"},
		IdempotencyKey: "catalog-review-safety-0001", Acknowledgment: Acknowledgment,
	}
	cases := []ReviewCommand{
		func() ReviewCommand { value := valid; value.Acknowledgment = "yes"; return value }(),
		func() ReviewCommand {
			value := valid
			value.Decision = "rejected"
			value.ReasonCodes = []string{"label_verified"}
			return value
		}(),
		func() ReviewCommand {
			value := valid
			value.Decision = "approved"
			value.ReasonCodes = nil
			return value
		}(),
		func() ReviewCommand { value := valid; value.IdempotencyKey = "short"; return value }(),
	}
	for _, command := range cases {
		if _, err := service.Submit(context.Background(), "alice", command); !errors.Is(err, ErrInvalidReview) {
			t.Fatalf("unsafe review was accepted: %+v err=%v", command, err)
		}
	}
	stale := valid
	stale.CatalogSHA256 = strings.Repeat("f", 64)
	if _, err := service.Submit(context.Background(), "alice", stale); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale catalog was accepted: %v", err)
	}
	staleGovernance := valid
	staleGovernance.GovernanceSHA256 = strings.Repeat("f", 64)
	if _, err := service.Submit(context.Background(), "alice", staleGovernance); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale governance was accepted: %v", err)
	}
	if _, err := service.Submit(context.Background(), "alice", valid); err != nil {
		t.Fatal(err)
	}
	conflict := valid
	conflict.Decision = "rejected"
	conflict.ReasonCodes = []string{"schema_issue"}
	if _, err := service.Submit(context.Background(), "alice", conflict); !errors.Is(err, ErrRevisionConflict) && !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay was accepted: %v", err)
	}
	staleRevision := valid
	staleRevision.IdempotencyKey = "catalog-review-safety-0002"
	if _, err := service.Submit(context.Background(), "alice", staleRevision); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision was accepted: %v", err)
	}
}

func TestServiceRejectsInvalidPaginationAndFilters(t *testing.T) {
	service := NewService(artifactStub{snapshot: reviewSnapshot()}, new(memoryRepository), time.Now)
	for _, query := range []Query{{Page: -1}, {PageSize: 21}, {Status: "forged"}, {Slice: "missing", Status: "all", Page: 1, PageSize: 1}} {
		if _, err := service.List(context.Background(), "alice", query); !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("invalid query was accepted: %+v err=%v", query, err)
		}
	}
}

func TestEvidenceSnapshotIsSelfHashedAndTracksLatestRevision(t *testing.T) {
	snapshot := reviewSnapshot()
	repository := new(memoryRepository)
	clockValue := time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC)
	service := NewService(artifactStub{snapshot: snapshot}, repository, func() time.Time { return clockValue })
	empty, err := service.Evidence(context.Background(), "alice")
	if err != nil || empty.ReviewedCases != 0 || empty.PendingCases != 3 || empty.ReadyForSealedMaterialization || len(empty.SnapshotSHA256) != 64 || len(empty.Entries) != 0 {
		t.Fatalf("unexpected empty evidence: %+v err=%v", empty, err)
	}
	if err := ValidateEvidenceSnapshot(empty, true); err != nil {
		t.Fatalf("empty evidence did not self-validate: %v", err)
	}
	command := ReviewCommand{CatalogSHA256: snapshot.CatalogSHA256, GovernanceSHA256: snapshot.Governance.ManifestSHA256, CaseID: "case-1", CaseSHA256: snapshot.Cases[0].CaseSHA256, ExpectedRevision: 0, Decision: "approved", ReasonCodes: []string{"label_verified"}, IdempotencyKey: "catalog-evidence-case-1-v1", Acknowledgment: Acknowledgment}
	if _, err := service.Submit(context.Background(), "alice", command); err != nil {
		t.Fatal(err)
	}
	partial, err := service.Evidence(context.Background(), "alice")
	if err != nil || partial.ReviewedCases != 1 || partial.ApprovedCases != 1 || partial.PendingCases != 2 || len(partial.Entries) != 1 || partial.Entries[0].CaseID != "case-1" || partial.LastReviewedAt == nil || !partial.LastReviewedAt.Equal(clockValue) {
		t.Fatalf("unexpected partial evidence: %+v err=%v", partial, err)
	}
	markdown, err := RenderEvidenceMarkdown(partial)
	if err != nil || !strings.Contains(markdown, "1/3") || !strings.Contains(markdown, partial.SnapshotSHA256) || strings.Contains(markdown, "first") {
		t.Fatalf("markdown evidence leaked payload or omitted proof: err=%v markdown=%s", err, markdown)
	}
	tampered := partial
	tampered.ApprovedCases = 2
	if err := ValidateEvidenceSnapshot(tampered, true); err == nil {
		t.Fatal("tampered evidence unexpectedly validated")
	}
	staleCase := partial
	staleCase.Entries = append([]EvidenceEntry(nil), partial.Entries...)
	staleCase.Entries[0].CaseSHA256 = strings.Repeat("f", 64)
	if err := FinalizeEvidenceSnapshot(&staleCase); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEvidenceAgainstSnapshot(staleCase, snapshot); err == nil {
		t.Fatal("self-consistent evidence with a stale case hash matched the catalog")
	}
}

func TestEvidenceSnapshotBecomesReadyOnlyWhenEveryCaseIsApproved(t *testing.T) {
	snapshot := reviewSnapshot()
	repository := new(memoryRepository)
	service := NewService(artifactStub{snapshot: snapshot}, repository, time.Now)
	for index, item := range snapshot.Cases {
		_, err := service.Submit(context.Background(), "alice", ReviewCommand{
			CatalogSHA256: snapshot.CatalogSHA256, GovernanceSHA256: snapshot.Governance.ManifestSHA256, CaseID: item.ID, CaseSHA256: item.CaseSHA256,
			ExpectedRevision: 0, Decision: "approved", ReasonCodes: []string{"label_verified"},
			IdempotencyKey: fmt.Sprintf("catalog-evidence-ready-%04d", index), Acknowledgment: Acknowledgment,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	report, err := service.Evidence(context.Background(), "alice")
	if err != nil || !report.ReadyForSealedMaterialization || report.Status != "ready_for_sealed_materialization" || report.ReviewedCases != 3 || report.ApprovedCases != 3 {
		t.Fatalf("complete evidence was not ready: %+v err=%v", report, err)
	}
}

func reviewSnapshot() Snapshot {
	return Snapshot{
		DatasetVersion: "dataset-v1", CatalogSHA256: strings.Repeat("a", 64),
		Governance: evalgovernance.Manifest{ManifestSHA256: strings.Repeat("b", 64)},
		Cases: []Case{
			{ID: "case-1", Slice: "intent", DatasetVersion: "slice-v1", CaseSHA256: strings.Repeat("1", 64), Prompt: "first", Content: map[string]any{"question": "first", "expected": map[string]any{"intent": "project_qa"}}},
			{ID: "case-2", Slice: "intent", DatasetVersion: "slice-v1", CaseSHA256: strings.Repeat("2", 64), Prompt: "second", Content: map[string]any{"question": "second"}},
			{ID: "case-3", Slice: "rag", DatasetVersion: "slice-v2", CaseSHA256: strings.Repeat("3", 64), Prompt: "third", Content: map[string]any{"question": "third"}},
		},
	}
}
