package evaluation

import (
	"GopherAI/internal/reliabilityeval"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestReliabilityAcceptanceEndpointIsExplicitlyIsolated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewReliabilityHandler(func(context.Context) (reliabilityeval.Report, error) {
		return reliabilityeval.Report{SchemaVersion: reliabilityeval.SchemaVersion, Mode: reliabilityeval.Mode, Simulation: true, Passed: true}, nil
	})
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/evaluations/reliability/acceptance", nil)
	handler.Acceptance(ginContext)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"simulation":true`) || !strings.Contains(recorder.Body.String(), `"mode":"isolated_in_process_fault_injection"`) {
		t.Fatalf("unexpected reliability response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestReliabilityAcceptancePersistsLatestReport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := reliabilityeval.NewFileStore(t.TempDir() + "/latest.json")
	handler := NewReliabilityHandlerWithStore(reliabilityeval.Run, store)
	accepted := httptest.NewRecorder()
	acceptedContext, _ := gin.CreateTestContext(accepted)
	acceptedContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/evaluations/reliability/acceptance", nil)
	handler.Acceptance(acceptedContext)
	if accepted.Code != http.StatusOK {
		t.Fatalf("acceptance failed: %d %s", accepted.Code, accepted.Body.String())
	}
	latest := httptest.NewRecorder()
	latestContext, _ := gin.CreateTestContext(latest)
	latestContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/evaluations/reliability/latest", nil)
	handler.Latest(latestContext)
	if latest.Code != http.StatusOK || !strings.Contains(latest.Body.String(), `"report_sha256"`) {
		t.Fatalf("latest report unavailable: %d %s", latest.Code, latest.Body.String())
	}
}
