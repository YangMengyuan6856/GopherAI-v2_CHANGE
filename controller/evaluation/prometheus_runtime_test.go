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

type fakePrometheusRuntimeReader struct {
	snapshot observability.PrometheusRuntimeSnapshot
	err      error
}

func (reader fakePrometheusRuntimeReader) Snapshot(context.Context) (observability.PrometheusRuntimeSnapshot, error) {
	return reader.snapshot, reader.err
}

func TestPrometheusRuntimeEndpointReturnsSanitizedProductionSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewPrometheusRuntimeHandler(fakePrometheusRuntimeReader{snapshot: observability.PrometheusRuntimeSnapshot{
		SchemaVersion: observability.PrometheusRuntimeSchemaVersion, Status: "ready", Source: "prometheus_http_api",
		CollectedAt: time.Date(2026, 9, 6, 1, 0, 0, 0, time.UTC), TargetCount: 2, HealthyTargetCount: 2, GroupCount: 4, RuleCount: 17,
	}})
	router := gin.New()
	router.GET("/metrics/runtime", handler.Latest)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics/runtime", nil))
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
	}
	for _, expected := range []string{`"status":"ready"`, `"source":"prometheus_http_api"`, `"target_count":2`, `"rule_count":17`} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("runtime response missing %s: %s", expected, recorder.Body.String())
		}
	}
}

func TestPrometheusRuntimeEndpointFailsClosedWithoutLeakingDependencyError(t *testing.T) {
	handler := NewPrometheusRuntimeHandler(fakePrometheusRuntimeReader{err: errors.New("dial secret-prometheus-host")})
	router := gin.New()
	router.GET("/metrics/runtime", handler.Latest)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics/runtime", nil))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "PROMETHEUS_RUNTIME_UNAVAILABLE") || strings.Contains(recorder.Body.String(), "secret-prometheus-host") {
		t.Fatalf("unexpected unavailable response: %d %s", recorder.Code, recorder.Body.String())
	}
}
