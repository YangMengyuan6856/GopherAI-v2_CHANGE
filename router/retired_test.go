package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRetiredSkillEntryIsMeasuredAndCannotExecute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/api/v1/skill/list", "/api/v1/skill/enable"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"name":"weather"}`))
		response := httptest.NewRecorder()
		InitRouter().ServeHTTP(response, request)
		if response.Code != http.StatusGone {
			t.Fatalf("retired route %q returned %d", path, response.Code)
		}
		body := response.Body.String()
		if !strings.Contains(body, "LEGACY_SKILL_RETIRED") || !strings.Contains(body, "/api/v1/tools/catalog") {
			t.Fatalf("retired route %q returned an unstable response: %s", path, body)
		}
	}
}

func TestUnknownRouteRemainsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest(http.MethodGet, "/not-a-real-route", nil)
	response := httptest.NewRecorder()
	InitRouter().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown route returned %d", response.Code)
	}
}
