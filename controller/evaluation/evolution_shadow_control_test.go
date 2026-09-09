package evaluation

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"GopherAI/internal/evolution"

	"github.com/gin-gonic/gin"
)

type fakeEvolutionShadowControlService struct {
	audit     evolution.ShadowControlAudit
	receipt   evolution.ShadowControlReceipt
	err       error
	principal string
}

func (service *fakeEvolutionShadowControlService) Audit(_ context.Context, principal string) (evolution.ShadowControlAudit, error) {
	service.principal = principal
	return service.audit, service.err
}

func (service *fakeEvolutionShadowControlService) RequestShadow(_ context.Context, principal string, _ evolution.ShadowControlCommand) (evolution.ShadowControlReceipt, error) {
	service.principal = principal
	return service.receipt, service.err
}

func (service *fakeEvolutionShadowControlService) Rollback(_ context.Context, principal string, _ evolution.ShadowControlCommand) (evolution.ShadowControlReceipt, error) {
	service.principal = principal
	return service.receipt, service.err
}

func TestEvolutionShadowControlHandlerStrictlyDecodesAndUsesJWTPrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeEvolutionShadowControlService{receipt: evolution.ShadowControlReceipt{SchemaVersion: evolution.ShadowControlSchemaVersion, AffectsLiveTraffic: false}}
	handler := NewEvolutionShadowControlHandler(service)
	router := gin.New()
	router.Use(func(ctx *gin.Context) { ctx.Set("userName", "alice"); ctx.Next() })
	router.POST("/shadow", handler.RequestShadow)
	body := fmt.Sprintf(`{"mode":"governed_isolated_shadow","experiment_version":"hexp-123456","artifact_type":"prompt_template","candidate_version":"candidate-v2","candidate_sha256":"%s","report_sha256":"%s","expected_state_version":0,"idempotency_key":"shadow-control-handler-0001","acknowledgment":"I_CONFIRM_HARNESS_CONTROL_OPERATION"}`, strings.Repeat("a", 64), strings.Repeat("b", 64))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/shadow", strings.NewReader(body)))
	if response.Code != http.StatusOK || service.principal != "alice" || !strings.Contains(response.Body.String(), `"affects_live_traffic":false`) {
		t.Fatalf("unexpected shadow control response: %d %s", response.Code, response.Body.String())
	}
	unknown := httptest.NewRecorder()
	router.ServeHTTP(unknown, httptest.NewRequest(http.MethodPost, "/shadow", strings.NewReader(strings.TrimSuffix(body, "}")+`,"force":true}`)))
	if unknown.Code != http.StatusBadRequest || !strings.Contains(unknown.Body.String(), "HARNESS_SHADOW_CONTROL_REQUEST_INVALID") {
		t.Fatalf("unknown field crossed control boundary: %d %s", unknown.Code, unknown.Body.String())
	}
}

func TestEvolutionShadowControlHandlerMapsPermissionDenial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeEvolutionShadowControlService{err: evolution.ErrPromotionPermissionDenied}
	handler := NewEvolutionShadowControlHandler(service)
	router := gin.New()
	router.POST("/rollback", handler.Rollback)
	body := `{"mode":"governed_isolated_shadow","artifact_type":"prompt_template","expected_state_version":0,"idempotency_key":"shadow-control-handler-0002","acknowledgment":"I_CONFIRM_HARNESS_CONTROL_OPERATION"}`
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/rollback", strings.NewReader(body)))
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "HARNESS_SHADOW_CONTROL_PERMISSION_DENIED") {
		t.Fatalf("permission denial was not preserved: %d %s", response.Code, response.Body.String())
	}
}
