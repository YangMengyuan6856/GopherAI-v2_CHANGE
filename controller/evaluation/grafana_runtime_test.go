package evaluation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"GopherAI/internal/observability"

	"github.com/gin-gonic/gin"
)

type fakeGrafanaRuntimeReader struct {
	snapshot observability.GrafanaRuntimeSnapshot
	err      error
}

func (reader fakeGrafanaRuntimeReader) Snapshot(context.Context) (observability.GrafanaRuntimeSnapshot, error) {
	return reader.snapshot, reader.err
}

func TestGrafanaRuntimeEndpointReturnsOnlySanitizedSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewGrafanaRuntimeHandler(fakeGrafanaRuntimeReader{snapshot: observability.GrafanaRuntimeSnapshot{
		SchemaVersion: observability.GrafanaRuntimeSchemaVersion, Status: "ready", Source: "grafana_loopback_health_and_provisioned_assets",
		GrafanaVersion: observability.ExpectedGrafanaVersion, Database: "ok", BindAddress: "container-private:9093",
		Dashboard: observability.GrafanaDashboardContract{UID: observability.GrafanaDashboardUID, PanelCount: 20, QueryCount: 20, Passed: true},
	}})
	router := gin.New()
	router.GET("/metrics/dashboard", handler.Latest)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics/dashboard", nil))
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
	}
	for _, expected := range []string{`"status":"ready"`, `"grafana_version":"13.2.1"`, `"uid":"gopherai-closed-loop-v1"`, `"public_exposure":false`} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("runtime response missing %s: %s", expected, recorder.Body.String())
		}
	}
}

func TestGrafanaRuntimeEndpointFailsClosedWithoutLeakingDependencyError(t *testing.T) {
	handler := NewGrafanaRuntimeHandler(fakeGrafanaRuntimeReader{err: errors.New("dial private-grafana-secret")})
	router := gin.New()
	router.GET("/metrics/dashboard", handler.Latest)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics/dashboard", nil))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "GRAFANA_RUNTIME_UNAVAILABLE") || strings.Contains(recorder.Body.String(), "private-grafana-secret") {
		t.Fatalf("unexpected unavailable response: %d %s", recorder.Code, recorder.Body.String())
	}
}
