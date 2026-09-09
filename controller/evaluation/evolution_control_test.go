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

func TestEvolutionControlAcceptancePersistsNoWriteReport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := evolution.NewFileControlAcceptanceStore(filepath.Join(t.TempDir(), "control.json"))
	handler := NewEvolutionControlHandler(func(ctx context.Context, _ time.Time) (evolution.ControlAcceptanceReport, error) {
		return evolution.RunControlAcceptance(ctx, time.Unix(900, 0))
	}, store)
	router := gin.New()
	router.POST("/acceptance", handler.Acceptance)
	router.GET("/latest", handler.Latest)

	run := httptest.NewRecorder()
	router.ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/acceptance", nil))
	if run.Code != http.StatusOK || !strings.Contains(run.Body.String(), `"passed_count":10`) || !strings.Contains(run.Body.String(), `"production_writes":0`) {
		t.Fatalf("unexpected control acceptance: %d %s", run.Code, run.Body.String())
	}
	latest := httptest.NewRecorder()
	router.ServeHTTP(latest, httptest.NewRequest(http.MethodGet, "/latest", nil))
	if latest.Code != http.StatusOK || !strings.Contains(latest.Body.String(), `"production_active_pointers":0`) {
		t.Fatalf("stored control acceptance unavailable: %d %s", latest.Code, latest.Body.String())
	}
}
