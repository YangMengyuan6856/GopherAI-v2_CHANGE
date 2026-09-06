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

type fakeEvolutionPromotionService struct {
	audit      evolution.PromotionAudit
	receipt    evolution.PromotionReviewReceipt
	reviewErr  error
	reviewUser string
}

func (service *fakeEvolutionPromotionService) Audit(context.Context, string) (evolution.PromotionAudit, error) {
	return service.audit, nil
}

func (service *fakeEvolutionPromotionService) Review(_ context.Context, user string, _ evolution.PromotionReviewCommand) (evolution.PromotionReviewReceipt, error) {
	service.reviewUser = user
	return service.receipt, service.reviewErr
}

func TestEvolutionPromotionHandlerUsesSeparatePermissionAndStrictBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeEvolutionPromotionService{reviewErr: evolution.ErrPromotionPermissionDenied}
	handler := NewEvolutionPromotionHandler(service)
	router := gin.New()
	router.Use(func(context *gin.Context) { context.Set("userName", "alice"); context.Next() })
	router.POST("/review", handler.Review)
	body := fmt.Sprintf("{\"mode\":\"human_gate_no_activation\",\"experiment_version\":\"hexp-123456\",\"candidate_sha256\":\"%s\",\"report_sha256\":\"%s\",\"decision\":\"rejected\",\"reason_code\":\"candidate_no_measured_gain\",\"idempotency_key\":\"promotion-review-key-0001\",\"acknowledgment\":\"I_CONFIRM_HUMAN_PROMOTION_REVIEW\"}", strings.Repeat("a", 64), strings.Repeat("b", 64))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/review", strings.NewReader(body)))
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "HARNESS_PROMOTION_PERMISSION_DENIED") || service.reviewUser != "alice" {
		t.Fatalf("separate reviewer permission was not enforced: %d %s", response.Code, response.Body.String())
	}

	unknown := httptest.NewRecorder()
	router.ServeHTTP(unknown, httptest.NewRequest(http.MethodPost, "/review", strings.NewReader(strings.TrimSuffix(body, "}")+",\"active\":true}")))
	if unknown.Code != http.StatusBadRequest || !strings.Contains(unknown.Body.String(), "HARNESS_PROMOTION_REQUEST_INVALID") {
		t.Fatalf("unknown field was accepted: %d %s", unknown.Code, unknown.Body.String())
	}
}
