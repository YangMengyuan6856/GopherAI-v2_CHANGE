package evaluation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	evaldomain "GopherAI/internal/evaluation"

	"github.com/gin-gonic/gin"
)

type pairedCollaborationStoreStub struct {
	report evaldomain.CollaborationABReport
}

func (stub pairedCollaborationStoreStub) Load(context.Context) (evaldomain.CollaborationABReport, string, error) {
	return stub.report, "collaboration-sha", nil
}

type pairedParentStoreStub struct {
	report evaldomain.ParentContextABReport
}

func (stub pairedParentStoreStub) Load(context.Context) (evaldomain.ParentContextABReport, string, error) {
	return stub.report, "parent-sha", nil
}

func TestPairedHandlerPublishesStatisticalMethodsWithoutCaseContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	collaboration := validPairedCollaborationReport()
	parent := validPairedParentReport()
	handler := NewPairedHandler(pairedCollaborationStoreStub{collaboration}, pairedParentStoreStub{parent})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/evaluations/paired/latest", nil)
	handler.Latest(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response PairedSummaryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Comparisons) != 2 || response.PromotionEligible || response.HumanReviewed || response.AnalysisSHA256 == "" {
		t.Fatalf("unexpected paired response: %+v", response)
	}
	first := response.Comparisons[0].Analysis
	if first.PairCount != 10 || first.CandidateSuccess.Numerator != 8 || first.BaselineSuccess.Numerator != 0 || first.Conclusion != "candidate_better" {
		t.Fatalf("unexpected collaboration statistics: %+v", first)
	}
	if body := recorder.Body.String(); containsAny(body, []string{"question", "answer", "case-"}) {
		t.Fatalf("summary leaked case content: %s", body)
	}
}

func validPairedCollaborationReport() evaldomain.CollaborationABReport {
	report := evaldomain.CollaborationABReport{
		SchemaVersion: "collaboration-paired-ab-v1", EvaluatorVersion: evaldomain.CollaborationEvaluatorVersion, DatasetVersion: evaldomain.CollaborationDatasetVersion,
		CandidateVersion: "candidate", GeneratedAt: time.Unix(1, 0), HumanReviewed: false, TechnicalGatesPassed: true, NetBenefitPassed: true,
		Metrics: evaldomain.CollaborationABMetrics{CaseCount: 20, TargetCaseCount: 10, SimpleGuardCaseCount: 10},
	}
	for index := 0; index < 20; index++ {
		item := evaldomain.CollaborationCaseResult{Slice: evaldomain.CollaborationSliceGuard}
		if index < 10 {
			item.Slice = evaldomain.CollaborationSliceTarget
			item.BaselineQuality = .6
			item.CandidateQuality = 1
			if index >= 8 {
				item.CandidateQuality = .6
			}
		}
		report.Cases = append(report.Cases, item)
	}
	return report
}

func validPairedParentReport() evaldomain.ParentContextABReport {
	report := evaldomain.ParentContextABReport{
		SchemaVersion: "parent-context-paired-ab-v1", EvaluatorVersion: evaldomain.ParentContextEvaluatorVersion, DatasetVersion: evaldomain.ParentContextDatasetVersion,
		CandidateVersion: "candidate", GeneratedAt: time.Unix(2, 0), HumanReviewed: false, TechnicalGatesPassed: true,
		Metrics: evaldomain.ParentContextABMetrics{CaseCount: 20, TargetCaseCount: 10, GuardCaseCount: 10},
	}
	for index := 0; index < 20; index++ {
		item := evaldomain.ParentContextCaseResult{Slice: evaldomain.ParentContextSliceGuard, BaselineQuality: .9, CandidateQuality: .9}
		if index < 10 {
			item.Slice = evaldomain.ParentContextSliceTarget
		}
		report.Cases = append(report.Cases, item)
	}
	return report
}

func containsAny(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if len(candidate) > 0 && len(value) >= len(candidate) {
			for index := 0; index+len(candidate) <= len(value); index++ {
				if value[index:index+len(candidate)] == candidate {
					return true
				}
			}
		}
	}
	return false
}
