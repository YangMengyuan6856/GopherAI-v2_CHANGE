package evaluation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestUnifiedLatestExposesReproducibleSummaryWithoutCasePayloads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewUnifiedHandler(NewFileUnifiedReportStore(filepath.Join("..", "..", "evals", "results", "devsupport-eval-run-v1-candidate.json")))
	router := gin.New()
	router.GET("/latest", handler.Latest)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/latest", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected report, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response UnifiedSummaryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Coverage.CatalogCases != 320 || response.Coverage.ExecutableCases != 300 || response.Coverage.CompletionRate != 1 {
		t.Fatalf("unexpected coverage: %+v", response.Coverage)
	}
	if !response.Decision.TechnicalGatesPassed || response.Decision.HumanReviewed || response.Decision.BaselineEligible || response.Decision.DefaultTrafficEligible {
		t.Fatalf("unexpected decision: %+v", response.Decision)
	}
	if containsText(recorder.Body.String(), `"question"`) || containsText(recorder.Body.String(), `"case_ids"`) {
		t.Fatalf("summary leaked per-case payload: %s", recorder.Body.String())
	}
}
