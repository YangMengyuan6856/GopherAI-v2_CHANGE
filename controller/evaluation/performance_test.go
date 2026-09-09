package evaluation

import (
	"GopherAI/internal/perfeval"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type performanceStoreStub struct {
	report perfeval.Report
	err    error
}

func (store performanceStoreStub) Load() (perfeval.Report, error) { return store.report, store.err }

func TestPerformanceLatestReturnsOnlyValidatedStoredSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewPerformanceHandler(performanceStoreStub{report: perfeval.Report{SchemaVersion: perfeval.SchemaVersion, GeneratedAt: time.Unix(100, 0), ReportSHA256: strings.Repeat("a", 64)}})
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/evaluations/performance/latest", nil)
	handler.Latest(context)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"schema_version":"performance-evaluation-report-v1"`) || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestPerformanceLatestFailsClosedWhenReportMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewPerformanceHandler(performanceStoreStub{err: errors.New("missing")})
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/evaluations/performance/latest", nil)
	handler.Latest(context)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "PERFORMANCE_REPORT_NOT_READY") {
		t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
	}
}
