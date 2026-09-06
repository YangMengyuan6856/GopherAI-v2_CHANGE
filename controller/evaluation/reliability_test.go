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
