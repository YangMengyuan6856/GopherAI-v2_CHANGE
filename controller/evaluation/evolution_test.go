package evaluation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"GopherAI/internal/evolution"

	"github.com/gin-gonic/gin"
)

type evolutionServiceStub struct {
	audit  evolution.Audit
	result evolution.MaterializeResult
}

func TestEvolutionSplitEndpointsKeepHoldoutSealed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := filepath.Join("..", "..", "evals")
	handler := NewEvolutionHandlerWithSplitPaths(evolutionServiceStub{}, filepath.Join(base, "devsupport-diagnostic-v1.jsonl"), filepath.Join(base, "devsupport-eval-v1.manifest.json"))
	router := gin.New()
	router.GET("/splits/latest", handler.SplitsLatest)
	router.POST("/splits/acceptance", handler.SplitsAcceptance)

	latest := httptest.NewRecorder()
	router.ServeHTTP(latest, httptest.NewRequest(http.MethodGet, "/splits/latest", nil))
	if latest.Code != http.StatusOK || !strings.Contains(latest.Body.String(), `"overlap_count":0`) || !strings.Contains(latest.Body.String(), `"holdout_open_api_available":false`) || strings.Contains(latest.Body.String(), "diag-v1-") {
		t.Fatalf("split audit leaked or failed: %d %s", latest.Code, latest.Body.String())
	}

	accepted := httptest.NewRecorder()
	router.ServeHTTP(accepted, httptest.NewRequest(http.MethodPost, "/splits/acceptance", strings.NewReader(`{"mode":"deterministic_no_write"}`)))
	if accepted.Code != http.StatusOK || !strings.Contains(accepted.Body.String(), `"passed":true`) || !strings.Contains(accepted.Body.String(), `"new_experiment_required"`) {
		t.Fatalf("unexpected split acceptance response: %d %s", accepted.Code, accepted.Body.String())
	}
}

func (stub evolutionServiceStub) Audit(context.Context) (evolution.Audit, error) {
	return stub.audit, nil
}
func (stub evolutionServiceStub) MaterializeLatest(context.Context) (evolution.MaterializeResult, error) {
	return stub.result, nil
}

func TestEvolutionEndpointsExposeOfflineBoundaryAndStrictSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	audit := evolution.Audit{SchemaVersion: evolution.SchemaVersion, Mode: evolution.Mode, ActivePointers: 0, AppliedCount: 0}
	handler := NewEvolutionHandler(evolutionServiceStub{audit: audit, result: evolution.MaterializeResult{SchemaVersion: evolution.SchemaVersion, Audit: audit}})
	router := gin.New()
	router.GET("/latest", handler.Latest)
	router.POST("/materialize", handler.Materialize)

	latest := httptest.NewRecorder()
	router.ServeHTTP(latest, httptest.NewRequest(http.MethodGet, "/latest", nil))
	if latest.Code != http.StatusOK || !strings.Contains(latest.Body.String(), `"active_pointers":0`) {
		t.Fatalf("unexpected audit response: %d %s", latest.Code, latest.Body.String())
	}

	accepted := httptest.NewRecorder()
	router.ServeHTTP(accepted, httptest.NewRequest(http.MethodPost, "/materialize", strings.NewReader(`{"source":"latest_failure_pool"}`)))
	if accepted.Code != http.StatusOK || !strings.Contains(accepted.Body.String(), `"schema_version":"harness-evolution-lineage-v1"`) {
		t.Fatalf("unexpected materialize response: %d %s", accepted.Code, accepted.Body.String())
	}

	rejected := httptest.NewRecorder()
	router.ServeHTTP(rejected, httptest.NewRequest(http.MethodPost, "/materialize", strings.NewReader(`{"source":"raw_chat_logs"}`)))
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), "INVALID_EVOLUTION_SOURCE") {
		t.Fatalf("unsafe source was accepted: %d %s", rejected.Code, rejected.Body.String())
	}
}
