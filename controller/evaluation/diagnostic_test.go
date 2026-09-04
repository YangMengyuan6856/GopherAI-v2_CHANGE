package evaluation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	evaldomain "GopherAI/internal/evaluation"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

func validTestReport() evaldomain.DiagnosticEvaluationReport {
	return evaldomain.DiagnosticEvaluationReport{
		EvaluatorVersion: evaldomain.DiagnosticEvaluatorVersion,
		DatasetVersion:   "devsupport-diagnostic-v1",
		GeneratedAt:      time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC),
		HumanReviewed:    false, BaselineEligible: false, TechnicalGatesPassed: true,
		Metrics: evaldomain.DiagnosticEvaluationMetrics{CaseCount: 1, RootCauseTop3Recall: 1},
		Cases:   []evaldomain.DiagnosticEvaluationCaseResult{{ID: "private-case", RootCauseHit: true}},
	}
}

func writeTestReport(t *testing.T, report evaldomain.DiagnosticEvaluationReport) string {
	t.Helper()
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFileReportStoreValidatesAndHashesReport(t *testing.T) {
	report, digest, err := NewFileReportStore(writeTestReport(t, validTestReport())).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Metrics.CaseCount != 1 || len(digest) != 64 {
		t.Fatalf("unexpected report or digest: %+v %q", report.Metrics, digest)
	}
	invalid := validTestReport()
	invalid.BaselineEligible = true
	if _, _, err := NewFileReportStore(writeTestReport(t, invalid)).Load(context.Background()); err == nil {
		t.Fatal("unreviewed report was accepted as a baseline")
	}
}

func TestLatestDiagnosticReturnsAuditableSummaryWithoutCases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(requestid.Attach())
	engine.GET("/latest", NewHandler(NewFileReportStore(writeTestReport(t, validTestReport()))).LatestDiagnostic)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/latest", nil))

	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") == "" {
		t.Fatalf("unexpected response %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if strings.Contains(body, "private-case") || strings.Contains(body, `"cases"`) {
		t.Fatalf("per-case report leaked through summary endpoint: %s", body)
	}
	var response SummaryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != responseSchemaVersion || response.BaselineEligible || response.HumanReviewed || len(response.Limitations) != 3 || len(response.ReportSHA256) != 64 {
		t.Fatalf("candidate limitations or identity missing: %+v", response)
	}

	cached := httptest.NewRecorder()
	cachedRequest := httptest.NewRequest(http.MethodGet, "/latest", nil)
	cachedRequest.Header.Set("If-None-Match", recorder.Header().Get("ETag"))
	engine.ServeHTTP(cached, cachedRequest)
	if cached.Code != http.StatusNotModified {
		t.Fatalf("expected ETag cache hit, got %d", cached.Code)
	}
}

func TestLatestDiagnosticFailsClosedForInvalidReport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(requestid.Attach())
	engine.GET("/latest", NewHandler(NewFileReportStore(filepath.Join(t.TempDir(), "missing.json"))).LatestDiagnostic)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/latest", nil))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "DIAGNOSTIC_EVALUATION_UNAVAILABLE") || strings.Contains(recorder.Body.String(), "missing.json") {
		t.Fatalf("unexpected fail-closed response: %d %s", recorder.Code, recorder.Body.String())
	}
}
