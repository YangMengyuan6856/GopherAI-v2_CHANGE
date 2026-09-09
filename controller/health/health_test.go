package health

import (
	healthservice "GopherAI/internal/health"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type fakeService struct {
	ready bool
}

func (service fakeService) Live() healthservice.LiveResponse {
	return healthservice.LiveResponse{Status: healthservice.StatusAlive, Service: "gopherai"}
}

func (service fakeService) Ready(context.Context) (healthservice.ReadyResponse, bool) {
	status := healthservice.StatusReady
	if !service.ready {
		status = healthservice.StatusNotReady
	}
	return healthservice.ReadyResponse{Status: status, Service: "gopherai"}, service.ready
}

func TestLiveReturnsOK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/health/live", nil)

	NewController(fakeService{ready: true}).Live(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, recorder.Code)
	}
}

func TestReadyReturnsServiceUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/health/ready", nil)

	NewController(fakeService{ready: false}).Ready(ctx)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected %d, got %d", http.StatusServiceUnavailable, recorder.Code)
	}
}
