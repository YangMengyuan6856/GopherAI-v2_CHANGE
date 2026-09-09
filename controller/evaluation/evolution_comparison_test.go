package evaluation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/evolution"

	"github.com/gin-gonic/gin"
)

func TestEvolutionComparisonPersistsAndReusesSameExperiment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := filepath.Join("..", "..", "evals")
	runner := func(ctx context.Context, existing *evolution.ComparisonReport, now time.Time) (evolution.ComparisonReport, bool, error) {
		return evolution.RunFairComparison(ctx, filepath.Join(base, "devsupport-diagnostic-v1.jsonl"), filepath.Join(base, "devsupport-eval-v1.manifest.json"), existing, now)
	}
	store := evolution.NewFileComparisonStore(filepath.Join(t.TempDir(), "comparison.json"))
	handler := NewEvolutionComparisonHandler(runner, store)
	router := gin.New()
	router.POST("/run", handler.Run)
	router.GET("/latest", handler.Latest)

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/run", strings.NewReader(`{"mode":"controlled_offline_contract_comparison"}`)))
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"created":true`) || !strings.Contains(first.Body.String(), `"state":"sealed"`) || !strings.Contains(first.Body.String(), `"decision":"rejected"`) {
		t.Fatalf("unexpected first comparison: %d %s", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/run", strings.NewReader(`{"mode":"controlled_offline_contract_comparison"}`)))
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `"reused":true`) || !strings.Contains(second.Body.String(), `"open_count":0`) {
		t.Fatalf("same experiment was not reused: %d %s", second.Code, second.Body.String())
	}

	latest := httptest.NewRecorder()
	router.ServeHTTP(latest, httptest.NewRequest(http.MethodGet, "/latest", nil))
	if latest.Code != http.StatusOK || strings.Contains(latest.Body.String(), "diag-v1-") || !strings.Contains(latest.Body.String(), `"production_candidate":false`) {
		t.Fatalf("latest report leaked cases or origin: %d %s", latest.Code, latest.Body.String())
	}
}
