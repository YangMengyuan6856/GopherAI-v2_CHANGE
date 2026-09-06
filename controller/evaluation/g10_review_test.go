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

	"github.com/gin-gonic/gin"
)

type stubInterviewEvidenceService struct {
	report    InterviewEvidencePackage
	err       error
	principal string
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
