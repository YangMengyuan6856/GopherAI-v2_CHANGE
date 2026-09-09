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

func validMemoryTestReport() evaldomain.MemoryEvaluationReport {
	cases := make([]evaldomain.MemoryEvaluationCaseResult, 20)
	for index := range cases {
		cases[index] = evaldomain.MemoryEvaluationCaseResult{ID: "private-memory-case", WithinBudget: true, Deterministic: true}
	}
	return evaldomain.MemoryEvaluationReport{
		EvaluatorVersion: evaldomain.MemoryEvaluatorVersion, DatasetVersion: evaldomain.MemoryDatasetVersion,
		GeneratedAt: time.Date(2026, 9, 5, 4, 30, 0, 0, time.UTC), HumanReviewed: false,
		BaselineEligible: false, TechnicalGatesPassed: true,
		Metrics: evaldomain.MemoryEvaluationMetrics{CaseCount: 20, RelevantMemoryRecall: 1, ContextBudgetPassRate: 1, DeterministicReplayRate: 1},
		Cases:   cases,
	}
}

func writeMemoryTestReport(t *testing.T, report evaldomain.MemoryEvaluationReport) string {
	t.Helper()
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "memory-report.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLatestMemoryReturnsSummaryWithoutPerCaseData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(requestid.Attach())
	engine.GET("/latest", NewMemoryHandler(NewFileMemoryReportStore(writeMemoryTestReport(t, validMemoryTestReport()))).LatestMemory)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/latest", nil))
	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") == "" || strings.Contains(recorder.Body.String(), "private-memory-case") || strings.Contains(recorder.Body.String(), `"cases"`) {
		t.Fatalf("unexpected memory summary %d: %s", recorder.Code, recorder.Body.String())
	}
	var response MemorySummaryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != memoryResponseSchemaVersion || response.HumanReviewed || response.BaselineEligible || len(response.Limitations) != 3 || len(response.ReportSHA256) != 64 {
		t.Fatalf("memory report limitations or identity missing: %+v", response)
	}
}

func TestMemoryReportStoreRejectsUnreviewedBaseline(t *testing.T) {
	report := validMemoryTestReport()
	report.BaselineEligible = true
	if _, _, err := NewFileMemoryReportStore(writeMemoryTestReport(t, report)).Load(context.Background()); err == nil {
		t.Fatal("unreviewed memory report was accepted as baseline")
	}
}
