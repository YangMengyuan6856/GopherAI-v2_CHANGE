package evaluation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"GopherAI/internal/controlrecommendation"

	"github.com/gin-gonic/gin"
)

type fakeRecommendationController struct {
	audit      controlrecommendation.AuditSnapshot
	acceptance controlrecommendation.AcceptanceResult
}

func (controller *fakeRecommendationController) Audit(context.Context) (controlrecommendation.AuditSnapshot, error) {
	return controller.audit, nil
}

func (controller *fakeRecommendationController) Acceptance(_ context.Context, _ string) (controlrecommendation.AcceptanceResult, error) {
	return controller.acceptance, nil
}

func TestRecommendationLatestAndAcceptanceExposeOnlySafeSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeRecommendationController{
		audit:      controlrecommendation.AuditSnapshot{SchemaVersion: controlrecommendation.SchemaVersion, Mode: controlrecommendation.ModeRecommendOnly, Latest: []controlrecommendation.RecommendationSummary{}},
		acceptance: controlrecommendation.AcceptanceResult{SchemaVersion: controlrecommendation.SchemaVersion, Simulation: true, Scenario: controlrecommendation.AcceptanceScenario, ActivePolicyUnchanged: true},
	}
	handler := NewRecommendationHandler(fake)
	router := gin.New()
	router.GET("/latest", handler.Latest)
	router.POST("/acceptance", handler.Acceptance)

	latest := httptest.NewRecorder()
	router.ServeHTTP(latest, httptest.NewRequest(http.MethodGet, "/latest", nil))
	if latest.Code != http.StatusOK || strings.Contains(latest.Body.String(), "candidate_policy_json") || strings.Contains(latest.Body.String(), "evidence_json") {
		t.Fatalf("unsafe latest response: code=%d body=%s", latest.Code, latest.Body.String())
	}
	var snapshot controlrecommendation.AuditSnapshot
	if err := json.Unmarshal(latest.Body.Bytes(), &snapshot); err != nil || snapshot.Mode != controlrecommendation.ModeRecommendOnly {
		t.Fatalf("invalid latest response: %v %+v", err, snapshot)
	}

	accepted := httptest.NewRecorder()
	body := `{"scenario":"recommend_only_guardrails"}`
	router.ServeHTTP(accepted, httptest.NewRequest(http.MethodPost, "/acceptance", strings.NewReader(body)))
	if accepted.Code != http.StatusOK || !strings.Contains(accepted.Body.String(), `"active_policy_unchanged":true`) {
		t.Fatalf("unexpected acceptance response: code=%d body=%s", accepted.Code, accepted.Body.String())
	}
}

func TestRecommendationAcceptanceRejectsUnknownFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewRecommendationHandler(&fakeRecommendationController{})
	router := gin.New()
	router.POST("/acceptance", handler.Acceptance)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/acceptance", strings.NewReader(`{"scenario":"recommend_only_guardrails","activate":true}`)))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "INVALID_CONTROLLER_ACCEPTANCE") {
		t.Fatalf("unknown activation field was not rejected: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
