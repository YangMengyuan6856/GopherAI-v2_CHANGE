package evaluation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/catalogreview"
	"GopherAI/internal/catalogseal"

	"github.com/gin-gonic/gin"
)

type stubInterviewEvidenceService struct {
	report    InterviewEvidencePackage
	err       error
	principal string
}

type stubG10CatalogReviewSource struct {
	workbench catalogreview.Workbench
	err       error
	principal string
}

type stubG10CatalogSealSource struct {
	status    catalogseal.Status
	err       error
	principal string
}

func (source *stubG10CatalogSealSource) Status(_ context.Context, principal string) (catalogseal.Status, error) {
	source.principal = principal
	return source.status, source.err
}

func (source *stubG10CatalogReviewSource) List(_ context.Context, principal string, _ catalogreview.Query) (catalogreview.Workbench, error) {
	source.principal = principal
	return source.workbench, source.err
}

func (service *stubInterviewEvidenceService) Build(_ context.Context, principal string) (InterviewEvidencePackage, error) {
	service.principal = principal
	return service.report, service.err
}

type stubG10ReviewService struct {
	report           G10ReleaseReview
	err              error
	principal        string
	confirmation     G10ResumeConfirmationReceipt
	confirmationErr  error
	confirmationUser string
	command          G10ResumeConfirmationCommand
}

func (service *stubG10ReviewService) Build(_ context.Context, principal string) (G10ReleaseReview, error) {
	service.principal = principal
	return service.report, service.err
}

func (service *stubG10ReviewService) ConfirmResumeFacts(_ context.Context, principal string, command G10ResumeConfirmationCommand) (G10ResumeConfirmationReceipt, error) {
	service.confirmationUser = principal
	service.command = command
	return service.confirmation, service.confirmationErr
}

func TestBuildG10ReleaseReviewPreservesBlockedGatesAndFacts(t *testing.T) {
	evidence, err := buildInterviewEvidencePackage(validInterviewEvidenceInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	report, err := buildG10ReleaseReview(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if report.ProductionReleaseReady || report.Status != "g10_blocked_by_human_and_environment_gates" || report.PassedGates != 1 || report.TotalGates != 4 {
		t.Fatalf("unexpected G10 state: %+v", report)
	}
	if report.ResumeFactCount != 8 || report.ExcludedFactCount != 4 || len(report.ResumeFactSetSHA256) != 64 || len(report.ReportSHA256) != 64 {
		t.Fatalf("unexpected fact accounting: %+v", report)
	}
	gateByID := map[string]G10ReviewGate{}
	for _, gate := range report.Gates {
		gateByID[gate.ID] = gate
	}
	if gateByID["resume_fact_confirmation"].Status != "pending_user" || !gateByID["resume_fact_confirmation"].UserConfirmationRequired {
		t.Fatalf("resume confirmation must remain a user gate: %+v", gateByID["resume_fact_confirmation"])
	}
	if gateByID["production_rollback_drill"].Status != "deferred_environment" || gateByID["cleanup_manifest_and_recovery"].Status != "passed" {
		t.Fatalf("environment and cleanup boundaries were not preserved: %+v", gateByID)
	}
	repeated, err := buildG10ReleaseReview(evidence)
	if err != nil || repeated.ReportSHA256 != report.ReportSHA256 {
		t.Fatalf("G10 report must be deterministic: %v %s != %s", err, repeated.ReportSHA256, report.ReportSHA256)
	}
}

func TestBuildG10ReleaseReviewRejectsTamperedEvidence(t *testing.T) {
	evidence, err := buildInterviewEvidencePackage(validInterviewEvidenceInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	evidence.Statements[0].Claim = "tampered"
	if _, err := buildG10ReleaseReview(evidence); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("expected evidence hash rejection, got %v", err)
	}
}

func TestG10ReviewServiceForwardsPrincipal(t *testing.T) {
	evidence, err := buildInterviewEvidencePackage(validInterviewEvidenceInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubInterviewEvidenceService{report: evidence}
	report, err := NewG10ReviewService(stub).Build(context.Background(), "alice")
	if err != nil || stub.principal != "alice" || report.ReleaseID != evidence.ReleaseID {
		t.Fatalf("unexpected service result: err=%v principal=%q report=%+v", err, stub.principal, report)
	}
}

func TestG10ReviewServiceAddsCurrentHumanGateProgressWithoutUnlockingProductGate(t *testing.T) {
	evidence, err := buildInterviewEvidencePackage(validInterviewEvidenceInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	reviewSource := &stubG10CatalogReviewSource{workbench: catalogreview.Workbench{
		SchemaVersion: catalogreview.SchemaVersion, DatasetVersion: "devsupport-eval-v1", CatalogSHA256: strings.Repeat("a", 64), Status: "human_review_in_progress",
		Progress: catalogreview.Progress{Total: 320, Reviewed: 12, Approved: 10, Rejected: 2, Pending: 308, ReviewSetSHA256: strings.Repeat("b", 64)},
	}}
	service := newG10ReviewService(&stubInterviewEvidenceService{report: evidence}, nil, time.Now)
	service.catalogReview = reviewSource
	sealSource := &stubG10CatalogSealSource{status: catalogseal.Status{
		SchemaVersion: catalogseal.StatusSchemaVersion, Status: "blocked_human_review", DatasetVersion: "devsupport-eval-v1",
		CatalogSHA256: strings.Repeat("a", 64), ReviewSetSHA: strings.Repeat("b", 64),
		Progress: catalogreview.Progress{Total: 320, Reviewed: 12, Approved: 10, Rejected: 2, Pending: 308, ReviewSetSHA256: strings.Repeat("b", 64)},
		NextGate: "complete human review",
	}}
	service.catalogSeal = sealSource
	report, err := service.Build(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if reviewSource.principal != "alice" || sealSource.principal != "alice" || report.HumanGateProgress == nil || report.HumanGateProgress.CatalogReview.Reviewed != 12 || report.HumanGateProgress.JudgeCalibration.Total != 30 {
		t.Fatalf("human progress missing: %+v", report.HumanGateProgress)
	}
	if report.PassedGates != 1 || report.ProductionReleaseReady || report.HumanGateProgress.SealedBaseline || report.HumanGateProgress.CatalogSealing.CandidateReady {
		t.Fatalf("queue progress must not unlock G10: %+v", report)
	}
	gate := map[string]G10ReviewGate{}
	for _, item := range report.Gates {
		gate[item.ID] = item
	}
	if !strings.Contains(gate["product_total_acceptance"].Conclusion, "12/320") || gate["product_total_acceptance"].Status != "blocked" {
		t.Fatalf("product gate does not expose bounded progress: %+v", gate["product_total_acceptance"])
	}
}

func TestG10SealedCandidateDoesNotBecomeSealedBaselineOrUnlockProductGate(t *testing.T) {
	evidence, err := buildInterviewEvidencePackage(validInterviewEvidenceInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	progress := catalogreview.Progress{Total: 320, Reviewed: 320, Approved: 320, ReviewSetSHA256: strings.Repeat("d", 64), ReadyForMaterializing: true}
	reviewSource := &stubG10CatalogReviewSource{workbench: catalogreview.Workbench{
		SchemaVersion: catalogreview.SchemaVersion, DatasetVersion: "devsupport-eval-v1", CatalogSHA256: strings.Repeat("c", 64), Status: "ready_for_sealed_materialization", Progress: progress,
	}}
	sealSource := &stubG10CatalogSealSource{status: catalogseal.Status{
		SchemaVersion: catalogseal.StatusSchemaVersion, Status: "sealed_candidate_ready", Eligible: true, DatasetVersion: "devsupport-eval-v1",
		CatalogSHA256: strings.Repeat("c", 64), ReviewSetSHA: strings.Repeat("d", 64), Progress: progress, NextGate: "rerun all evaluations",
		CurrentSeal: &catalogseal.Report{
			SchemaVersion: catalogseal.SchemaVersion, Status: "sealed_candidate_ready", SealID: "catalog-seal-" + strings.Repeat("e", 32), SealSHA256: strings.Repeat("f", 64),
			SourceCatalogSHA256: strings.Repeat("c", 64), SourceReviewSetSHA256: strings.Repeat("d", 64), CaseCount: 320, ApprovedCases: 320,
			OutputCatalogSHA256: strings.Repeat("1", 64), OutputReviewSHA256: strings.Repeat("2", 64),
		},
	}}
	service := newG10ReviewService(&stubInterviewEvidenceService{report: evidence}, nil, time.Now)
	service.catalogReview, service.catalogSeal = reviewSource, sealSource
	report, err := service.Build(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	human := report.HumanGateProgress
	if human == nil || !human.CatalogSealing.CandidateReady || !human.CatalogSealing.ArtifactIntegrityVerified || human.SealedBaseline || report.PassedGates != 1 || report.ProductionReleaseReady {
		t.Fatalf("sealed candidate crossed a forbidden gate: %+v", report)
	}
	var product G10ReviewGate
	for _, gate := range report.Gates {
		if gate.ID == "product_total_acceptance" {
			product = gate
		}
	}
	if product.Status != "blocked" || !strings.Contains(product.Conclusion, "正式基线证据尚未重跑冻结") || !strings.Contains(human.NextRequiredGate, "统一 Runner") {
		t.Fatalf("candidate boundary is not explicit: gate=%+v progress=%+v", product, human)
	}
}

func TestG10ReviewHandlerJSONMarkdownAndErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	evidence, err := buildInterviewEvidencePackage(validInterviewEvidenceInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	report, err := buildG10ReleaseReview(evidence)
	if err != nil {
		t.Fatal(err)
	}
	service := &stubG10ReviewService{report: report}
	handler := NewG10ReviewHandler(service)

	jsonRecorder := httptest.NewRecorder()
	jsonContext, _ := gin.CreateTestContext(jsonRecorder)
	jsonContext.Request = httptest.NewRequest(http.MethodGet, "/evaluations/g10/latest", nil)
	jsonContext.Set("userName", "alice")
	handler.Latest(jsonContext)
	if jsonRecorder.Code != http.StatusOK || service.principal != "alice" || !strings.Contains(jsonRecorder.Body.String(), `"production_release_ready":false`) {
		t.Fatalf("unexpected JSON response: %d %s", jsonRecorder.Code, jsonRecorder.Body.String())
	}
	var decoded G10ReleaseReview
	if err := json.Unmarshal(jsonRecorder.Body.Bytes(), &decoded); err != nil || decoded.ReportSHA256 != report.ReportSHA256 {
		t.Fatalf("invalid JSON report: %v %+v", err, decoded)
	}

	markdownRecorder := httptest.NewRecorder()
	markdownContext, _ := gin.CreateTestContext(markdownRecorder)
	markdownContext.Request = httptest.NewRequest(http.MethodGet, "/evaluations/g10/latest?format=markdown", nil)
	handler.Latest(markdownContext)
	if markdownRecorder.Code != http.StatusOK || !strings.Contains(markdownRecorder.Body.String(), "当前禁止写入简历的事实") || !strings.Contains(markdownRecorder.Header().Get("Content-Disposition"), ".md") {
		t.Fatalf("unexpected markdown response: %d %s", markdownRecorder.Code, markdownRecorder.Body.String())
	}
	var markdown bytes.Buffer
	if err := writeG10ReviewMarkdown(&markdown, report); err != nil || !strings.Contains(markdown.String(), report.ReportSHA256) {
		t.Fatalf("markdown writer failed: %v", err)
	}

	badRecorder := httptest.NewRecorder()
	badContext, _ := gin.CreateTestContext(badRecorder)
	badContext.Request = httptest.NewRequest(http.MethodGet, "/evaluations/g10/latest?format=html", nil)
	handler.Latest(badContext)
	if badRecorder.Code != http.StatusBadRequest || !strings.Contains(badRecorder.Body.String(), "INVALID_G10_REVIEW_FORMAT") {
		t.Fatalf("unexpected invalid-format response: %d %s", badRecorder.Code, badRecorder.Body.String())
	}

	errorHandler := NewG10ReviewHandler(&stubG10ReviewService{err: errors.New("not ready")})
	errorRecorder := httptest.NewRecorder()
	errorContext, _ := gin.CreateTestContext(errorRecorder)
	errorContext.Request = httptest.NewRequest(http.MethodGet, "/evaluations/g10/latest", nil)
	errorHandler.Latest(errorContext)
	if errorRecorder.Code != http.StatusServiceUnavailable || !strings.Contains(errorRecorder.Body.String(), "G10_REVIEW_NOT_READY") {
		t.Fatalf("unexpected unavailable response: %d %s", errorRecorder.Code, errorRecorder.Body.String())
	}
}
