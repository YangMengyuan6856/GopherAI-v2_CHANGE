package evaluation

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMetricCatalogEndpointPublishesBoundedCompleteSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewDefaultMetricCatalogHandler()
	router := gin.New()
	router.GET("/metrics/catalog", handler.Latest)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics/catalog", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected metric catalog, got %d: %s", recorder.Code, recorder.Body.String())
	}
	for _, expected := range []string{`"passed":true`, `"family_count":88`, `"required_family_count":46`, `"required_present_count":46`, `"contract_mismatch_count":0`, `"forbidden_label_hits":0`, `"duplicate_metric_names":0`, `"name":"backend"`, `"family_count":82`, `"name":"index_worker"`, `"family_count":6`} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("metric catalog response missing %s", expected)
		}
	}
}

func TestMetricCatalogEndpointSupportsETag(t *testing.T) {
	handler := NewDefaultMetricCatalogHandler()
	router := gin.New()
	router.GET("/metrics/catalog", handler.Latest)
	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/metrics/catalog", nil))
	request := httptest.NewRequest(http.MethodGet, "/metrics/catalog", nil)
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))
	second := httptest.NewRecorder()
	router.ServeHTTP(second, request)
	if second.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", second.Code)
	}
}
