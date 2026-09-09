package evaluation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	evaldomain "GopherAI/internal/evaluation"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

func validCollaborationTestReport() evaldomain.CollaborationABReport {
	cases := make([]evaldomain.CollaborationCaseResult, evaldomain.CollaborationCaseCount)
	for index := range cases {
		cases[index] = evaldomain.CollaborationCaseResult{ID: "private-collaboration-case", SafetyPassed: true}
	}
	return evaldomain.CollaborationABReport{
		SchemaVersion: "collaboration-paired-ab-v1", EvaluatorVersion: evaldomain.CollaborationEvaluatorVersion,
		DatasetVersion: evaldomain.CollaborationDatasetVersion, CandidateVersion: "candidate",
		GeneratedAt: time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC), TechnicalGatesPassed: true, NetBenefitPassed: true,
		DefaultTrafficEnabled: false, RecommendedDefaultWeight: 0,
		Metrics: evaldomain.CollaborationABMetrics{
			CaseCount: evaldomain.CollaborationCaseCount, TargetCaseCount: evaldomain.CollaborationTargetCaseCount,
			SimpleGuardCaseCount: evaldomain.CollaborationGuardCaseCount, MeanQualityDelta: .4,
		},
		Cases: cases,
	}
}

func writeCollaborationTestReport(t *testing.T, report evaldomain.CollaborationABReport) string {
	t.Helper()
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "collaboration-report.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLatestCollaborationReturnsSummaryWithoutQuestionsOrCases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(requestid.Attach())
	engine.GET("/latest", NewCollaborationHandler(NewFileCollaborationReportStore(writeCollaborationTestReport(t, validCollaborationTestReport()))).Latest)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/latest", nil))
	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") == "" || strings.Contains(recorder.Body.String(), "private-collaboration-case") || strings.Contains(recorder.Body.String(), `"cases"`) {
		t.Fatalf("unexpected collaboration summary %d: %s", recorder.Code, recorder.Body.String())
	}
	var response CollaborationSummaryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != collaborationResponseSchemaVersion || response.HumanReviewed || response.PromotionEligible || response.DefaultTrafficEnabled || response.RecommendedDefaultWeight != 0 || len(response.Limitations) != 3 || len(response.ReportSHA256) != 64 {
		t.Fatalf("collaboration report boundary missing: %+v", response)
	}
}

func TestCollaborationReportStoreRejectsUnreviewedPromotionOrTraffic(t *testing.T) {
	report := validCollaborationTestReport()
	report.PromotionEligible = true
	if _, _, err := NewFileCollaborationReportStore(writeCollaborationTestReport(t, report)).Load(context.Background()); err == nil {
		t.Fatal("unreviewed collaboration report was accepted for promotion")
	}
	report = validCollaborationTestReport()
	report.DefaultTrafficEnabled = true
	if _, _, err := NewFileCollaborationReportStore(writeCollaborationTestReport(t, report)).Load(context.Background()); err == nil {
		t.Fatal("collaboration candidate traffic was accepted")
	}
}
