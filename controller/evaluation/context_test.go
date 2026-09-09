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

func validContextTestReport() evaldomain.ContextEvaluationReport {
	cases := make([]evaldomain.ContextEvaluationCaseResult, 12)
	for index := range cases {
		cases[index] = evaldomain.ContextEvaluationCaseResult{ID: "private-context-case", Deterministic: true}
	}
	return evaldomain.ContextEvaluationReport{
		EvaluatorVersion: evaldomain.ContextEvaluatorVersion, DatasetVersion: evaldomain.ContextDatasetVersion,
		GeneratedAt: time.Date(2026, 9, 5, 5, 0, 0, 0, time.UTC), TechnicalGatesPassed: true,
		Metrics: evaldomain.ContextEvaluationMetrics{CaseCount: 12, ConstraintRetention: 1, ConfirmedFactRetention: 1, OpenQuestionRetention: 1, NextActionRetention: 1, DeterministicReplayRate: 1},
		Cases:   cases,
	}
}

func writeContextTestReport(t *testing.T, report evaldomain.ContextEvaluationReport) string {
	t.Helper()
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "context-report.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLatestContextReturnsSummaryWithoutPerCaseData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(requestid.Attach())
	engine.GET("/latest", NewContextHandler(NewFileContextReportStore(writeContextTestReport(t, validContextTestReport()))).LatestContext)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/latest", nil))
	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") == "" || strings.Contains(recorder.Body.String(), "private-context-case") || strings.Contains(recorder.Body.String(), `"cases"`) {
		t.Fatalf("unexpected context summary %d: %s", recorder.Code, recorder.Body.String())
	}
	var response ContextSummaryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != contextResponseSchemaVersion || response.HumanReviewed || response.BaselineEligible || len(response.Limitations) != 3 || len(response.ReportSHA256) != 64 {
		t.Fatalf("context report limitations or identity missing: %+v", response)
	}
}

func TestContextReportStoreRejectsUnreviewedBaseline(t *testing.T) {
	report := validContextTestReport()
	report.BaselineEligible = true
	if _, _, err := NewFileContextReportStore(writeContextTestReport(t, report)).Load(context.Background()); err == nil {
		t.Fatal("unreviewed context report was accepted as baseline")
	}
}
