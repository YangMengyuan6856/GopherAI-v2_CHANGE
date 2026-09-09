package evaluation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"GopherAI/internal/faultcampaign"

	"github.com/gin-gonic/gin"
)

type fakeFaultCampaignService struct {
	audit  faultcampaign.AuditSnapshot
	report faultcampaign.CampaignReport
}

func (service *fakeFaultCampaignService) Audit(context.Context) (faultcampaign.AuditSnapshot, error) {
	return service.audit, nil
}
func (service *fakeFaultCampaignService) RunAcceptance(context.Context, string) (faultcampaign.CampaignReport, error) {
	return service.report, nil
}

func TestFaultCampaignEndpointsExposeObserveOnlyContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	report, err := faultcampaign.BuildReport()
	if err != nil {
		t.Fatal(err)
	}
	handler := NewFaultCampaignHandler(&fakeFaultCampaignService{audit: faultcampaign.AuditSnapshot{SchemaVersion: faultcampaign.SchemaVersion, Mode: faultcampaign.Mode}, report: report})
	router := gin.New()
	router.GET("/latest", handler.Latest)
	router.POST("/acceptance", handler.Acceptance)

	latest := httptest.NewRecorder()
	router.ServeHTTP(latest, httptest.NewRequest(http.MethodGet, "/latest", nil))
	if latest.Code != http.StatusOK || !strings.Contains(latest.Body.String(), `"mode":"observe_only"`) {
		t.Fatalf("unexpected audit response: %d %s", latest.Code, latest.Body.String())
	}
	accepted := httptest.NewRecorder()
	router.ServeHTTP(accepted, httptest.NewRequest(http.MethodPost, "/acceptance", strings.NewReader(`{"scenario":"three_failure_classes"}`)))
	body := accepted.Body.String()
	if accepted.Code != http.StatusOK || !strings.Contains(body, `"scenario_count":3`) || !strings.Contains(body, `"applied_count":0`) || !strings.Contains(body, `"mitigation_success_state":"not_measured_observe_only"`) {
		t.Fatalf("unexpected acceptance response: %d %s", accepted.Code, body)
	}
}

func TestFaultCampaignRejectsActivationAndArbitraryFaultInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewFaultCampaignHandler(&fakeFaultCampaignService{})
	router := gin.New()
	router.POST("/acceptance", handler.Acceptance)
	for _, body := range []string{
		`{"scenario":"three_failure_classes","activate":true}`,
		`{"scenario":"three_failure_classes","command":"docker stop redis-vector"}`,
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/acceptance", strings.NewReader(body)))
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "INVALID_FAULT_CAMPAIGN") {
			t.Fatalf("unsafe request accepted: %d %s", recorder.Code, recorder.Body.String())
		}
	}
}
