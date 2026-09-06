package evaluation

import (
	"GopherAI/internal/onlineeval"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type onlineServiceStub struct {
	audit  onlineeval.Audit
	report onlineeval.AcceptanceReport
}

func (service onlineServiceStub) Audit(context.Context) (onlineeval.Audit, error) {
	return service.audit, nil
}

func (service onlineServiceStub) RunAcceptance(context.Context, string) (onlineeval.AcceptanceReport, error) {
	return service.report, nil
}

func TestOnlineEvaluationEndpointDoesNotExposeSamplePayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewOnlineEvaluationHandler(onlineServiceStub{audit: onlineeval.Audit{
		SchemaVersion: onlineeval.AuditSchemaVersion,
		Latest:        &onlineeval.PublicSample{ID: "sample-1", Status: onlineeval.StatusCompleted, Strategy: "rag_fast"},
	}})
	router := gin.New()
	router.GET("/online/latest", handler.Latest)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/online/latest", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"sample-1"`) {
		t.Fatalf("unexpected online evaluation response: %d %s", recorder.Code, recorder.Body.String())
	}
	for _, forbidden := range []string{"question", "answer", "evidence_json", "user_hash", "tenant_hash"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("online evaluation API exposed forbidden field %q: %s", forbidden, recorder.Body.String())
		}
	}
}
