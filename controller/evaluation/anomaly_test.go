package evaluation

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestAnomalySimulationSeparatesFixtureFromProductionAndNeverApplies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewAnomalyHandler()
	handler.now = func() time.Time { return time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC) }
	router := gin.New()
	router.POST("/simulate", handler.Simulate)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/simulate", strings.NewReader(`{"scenario":"quality_drop"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected simulation, got %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, expected := range []string{`"simulation":true`, `"source":"deterministic_acceptance_fixture"`, `"anomalous":true`, `"mode":"recommend_only"`, `"applied":false`, `"current_excluded":true`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("response missing %s: %s", expected, body)
		}
	}
}

func TestAnomalySimulationRejectsUnknownAndExtraFields(t *testing.T) {
	handler := NewAnomalyHandler()
	router := gin.New()
	router.POST("/simulate", handler.Simulate)
	for _, body := range []string{`{"scenario":"unknown"}`, `{"scenario":"healthy","policy_write":true}`} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/simulate", strings.NewReader(body))
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d", body, recorder.Code)
		}
	}
}

func TestAnomalySimulationExposesLowPopulationAsUndecided(t *testing.T) {
	handler := NewAnomalyHandler()
	router := gin.New()
	router.POST("/simulate", handler.Simulate)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/simulate", strings.NewReader(`{"scenario":"low_sample"}`))
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected simulation, got %d: %s", recorder.Code, recorder.Body.String())
	}
	for _, expected := range []string{`"decision_status":"insufficient_data"`, `"population":12`, `"minimum_population":50`, `"status":"suppressed"`, `"action":"none"`, `"applied":false`} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("low-population response missing %s: %s", expected, recorder.Body.String())
		}
	}
}
