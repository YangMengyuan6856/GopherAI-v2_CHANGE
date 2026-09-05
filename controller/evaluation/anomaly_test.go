package evaluation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/observability"

	"github.com/gin-gonic/gin"
)

type fakeProductionAnomalyReader struct {
	snapshot observability.ProductionAnomalySnapshot
	err      error
}

func (reader fakeProductionAnomalyReader) LatestProductionAnalysis(context.Context) (observability.ProductionAnomalySnapshot, error) {
	return reader.snapshot, reader.err
}

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

func TestProductionAnomalyEndpointIsExplicitlyNonSimulation(t *testing.T) {
	handler := NewAnomalyHandlerWithProduction(fakeProductionAnomalyReader{snapshot: observability.ProductionAnomalySnapshot{
		SchemaVersion: observability.ProductionAnomalySchemaVersion, Status: "warming", Source: "mysql_metric_window_snapshots", Simulation: false,
	}})
	router := gin.New()
	router.GET("/production/latest", handler.ProductionLatest)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/production/latest", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected production snapshot, got %d: %s", recorder.Code, recorder.Body.String())
	}
	for _, expected := range []string{`"schema_version":"production-anomaly-snapshot-v1"`, `"simulation":false`, `"source":"mysql_metric_window_snapshots"`, `"status":"warming"`} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("production response missing %s: %s", expected, recorder.Body.String())
		}
	}
}

func TestProductionAnomalyEndpointSanitizesRepositoryFailure(t *testing.T) {
	handler := NewAnomalyHandlerWithProduction(fakeProductionAnomalyReader{err: errors.New("mysql password leaked")})
	router := gin.New()
	router.GET("/production/latest", handler.ProductionLatest)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/production/latest", nil))
	if recorder.Code != http.StatusServiceUnavailable || strings.Contains(recorder.Body.String(), "password") {
		t.Fatalf("repository detail must be sanitized: %d %s", recorder.Code, recorder.Body.String())
	}
}
