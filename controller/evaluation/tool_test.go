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

func validToolTestReport() evaldomain.ToolEvaluationReport {
	cases := make([]evaldomain.ToolEvaluationCaseResult, 30)
	for index := range cases {
		cases[index] = evaldomain.ToolEvaluationCaseResult{ID: "private-tool-case", Passed: true, Deterministic: true}
	}
	return evaldomain.ToolEvaluationReport{
		EvaluatorVersion: evaldomain.ToolEvaluatorVersion, DatasetVersion: evaldomain.ToolDatasetVersion,
		GeneratedAt: time.Date(2026, 9, 5, 6, 0, 0, 0, time.UTC), TechnicalGatesPassed: true,
		Metrics: evaldomain.ToolEvaluationMetrics{CaseCount: 30, ToolSelectionAccuracy: 1, SchemaContractPassRate: 1, AuthorizationPolicyPassRate: 1, ResiliencePassRate: 1, SafetyPassRate: 1, AuditCoverageRate: 1, DeterministicReplayRate: 1},
		Cases:   cases,
	}
}

func writeToolTestReport(t *testing.T, report evaldomain.ToolEvaluationReport) string {
	t.Helper()
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tool-report.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLatestToolReturnsSummaryWithoutPerCaseData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(requestid.Attach())
	engine.GET("/latest", NewToolHandler(NewFileToolReportStore(writeToolTestReport(t, validToolTestReport()))).LatestTool)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/latest", nil))
	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") == "" || strings.Contains(recorder.Body.String(), "private-tool-case") || strings.Contains(recorder.Body.String(), `"cases"`) {
		t.Fatalf("unexpected tool summary %d: %s", recorder.Code, recorder.Body.String())
	}
	var response ToolSummaryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != toolResponseSchemaVersion || response.HumanReviewed || response.BaselineEligible || len(response.Limitations) != 3 || len(response.ReportSHA256) != 64 {
		t.Fatalf("tool report limitations or identity missing: %+v", response)
	}
}

func TestToolReportStoreRejectsUnreviewedBaseline(t *testing.T) {
	report := validToolTestReport()
	report.BaselineEligible = true
	if _, _, err := NewFileToolReportStore(writeToolTestReport(t, report)).Load(context.Background()); err == nil {
		t.Fatal("unreviewed tool report was accepted as baseline")
	}
}
