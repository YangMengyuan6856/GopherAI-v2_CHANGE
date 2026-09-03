package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLiveRouteIsPublic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	response := httptest.NewRecorder()

	InitRouter().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected public live route to return %d, got %d", http.StatusOK, response.Code)
	}
	if response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("expected JSON response, got %q", response.Header().Get("Content-Type"))
	}
}
