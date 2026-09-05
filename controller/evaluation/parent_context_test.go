package evaluation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	evaldomain "GopherAI/internal/evaluation"

	"github.com/gin-gonic/gin"
)

type parentContextStoreStub struct {
	report evaldomain.ParentContextABReport
}

func (stub parentContextStoreStub) Load(context.Context) (evaldomain.ParentContextABReport, string, error) {
	return stub.report, "abc123", nil
}

func TestParentContextLatestReturnsSummaryWithoutCasePayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	report := evaldomain.ParentContextABReport{
		SchemaVersion: "parent-context-paired-ab-v1", EvaluatorVersion: evaldomain.ParentContextEvaluatorVersion,
		DatasetVersion: evaldomain.ParentContextDatasetVersion, CandidateVersion: "candidate", GeneratedAt: time.Unix(1, 0),
		Metrics: evaldomain.ParentContextABMetrics{
			CaseCount: evaldomain.ParentContextCaseCount, TargetCaseCount: evaldomain.ParentContextTargetCaseCount,
			GuardCaseCount: evaldomain.ParentContextGuardCaseCount,
		},
		Cases: make([]evaldomain.ParentContextCaseResult, evaldomain.ParentContextCaseCount),
	}
	handler := NewParentContextHandler(parentContextStoreStub{report: report})
	router := gin.New()
	router.GET("/latest", handler.Latest)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/latest", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); containsText(body, `"cases"`) || !containsText(body, `"recommended_default_weight":0`) {
		t.Fatalf("summary must omit cases and preserve zero traffic: %s", body)
	}
}

func TestValidateParentContextReportRejectsAutomaticTraffic(t *testing.T) {
	report := evaldomain.ParentContextABReport{
		SchemaVersion: "parent-context-paired-ab-v1", EvaluatorVersion: evaldomain.ParentContextEvaluatorVersion,
		DatasetVersion: evaldomain.ParentContextDatasetVersion, GeneratedAt: time.Unix(1, 0), RecommendedDefaultWeight: 10,
		Metrics: evaldomain.ParentContextABMetrics{
			CaseCount: evaldomain.ParentContextCaseCount, TargetCaseCount: evaldomain.ParentContextTargetCaseCount,
			GuardCaseCount: evaldomain.ParentContextGuardCaseCount,
		},
		Cases: make([]evaldomain.ParentContextCaseResult, evaldomain.ParentContextCaseCount),
	}
	if err := validateParentContextReport(report); err == nil {
		t.Fatal("candidate report must not enable traffic")
	}
}

func containsText(value string, expected string) bool {
	for index := 0; index+len(expected) <= len(value); index++ {
		if value[index:index+len(expected)] == expected {
			return true
		}
	}
	return false
}
