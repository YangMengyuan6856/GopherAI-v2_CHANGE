package evaluation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCatalogLatestExposesReviewGateSeparatelyFromSchema(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewCatalogHandler(filepath.Join("..", "..", "evals", "devsupport-eval-v1.manifest.json"))
	router := gin.New()
	router.GET("/latest", handler.Latest)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/latest", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected valid catalog, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response CatalogSummaryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.SchemaPassed || response.ActualTotal != 320 || response.UniqueIDs != 320 || response.SensitiveHits != 0 {
		t.Fatalf("unexpected catalog summary: %+v", response)
	}
	if response.HumanReviewed || response.BaselineEligible {
		t.Fatalf("pending labels must not become a baseline: %+v", response)
	}
	if len(response.Slices) != 6 {
		t.Fatalf("expected six frozen slices, got %d", len(response.Slices))
	}
}
