package evaluation

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	evaldomain "GopherAI/internal/evaluation"
	"GopherAI/internal/judgecalibration"

	"github.com/gin-gonic/gin"
)

type judgeCalibrationServiceStub struct {
	audit     judgecalibration.Audit
	receipt   judgecalibration.ReviewReceipt
	reviewer  string
	caseID    string
	scores    evaldomain.JudgeScores
	submitErr error
}

func (stub *judgeCalibrationServiceStub) Audit(_ context.Context, reviewer string) (judgecalibration.Audit, error) {
	stub.reviewer = reviewer
	return stub.audit, nil
}

func (stub *judgeCalibrationServiceStub) Submit(_ context.Context, reviewer, caseID string, scores evaldomain.JudgeScores) (judgecalibration.ReviewReceipt, error) {
	stub.reviewer, stub.caseID, stub.scores = reviewer, caseID, scores
	return stub.receipt, stub.submitErr
}

func TestJudgeCalibrationHandlerScopesAuditAndReviewToAuthenticatedUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &judgeCalibrationServiceStub{
		audit:   judgecalibration.Audit{SchemaVersion: judgecalibration.SchemaVersion, CaseCount: 30},
		receipt: judgecalibration.ReviewReceipt{SchemaVersion: judgecalibration.ReviewSchemaVersion, CaseID: "judge-cal-001", Created: true, Revision: 1, ReviewedAt: time.Unix(1, 0)},
	}
	handler := NewJudgeCalibrationHandler(stub)

	latest := httptest.NewRecorder()
	latestContext, _ := gin.CreateTestContext(latest)
	latestContext.Set("userName", "alice")
	latestContext.Request = httptest.NewRequest(http.MethodGet, "/evaluations/judge-calibration/latest", nil)
	handler.Latest(latestContext)
	if latest.Code != http.StatusOK || stub.reviewer != "alice" || latest.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected latest response: code=%d reviewer=%s body=%s", latest.Code, stub.reviewer, latest.Body.String())
	}

	review := httptest.NewRecorder()
	reviewContext, _ := gin.CreateTestContext(review)
	reviewContext.Set("userName", "alice")
	reviewContext.Request = httptest.NewRequest(http.MethodPost, "/evaluations/judge-calibration/reviews", bytes.NewBufferString(`{"case_id":"judge-cal-001","scores":{"relevance":1,"completeness":0.75,"helpfulness":0.5,"groundedness":1,"safety":1}}`))
	reviewContext.Request.Header.Set("Content-Type", "application/json")
	handler.Review(reviewContext)
	if review.Code != http.StatusAccepted || stub.reviewer != "alice" || stub.caseID != "judge-cal-001" || stub.scores.Helpfulness != .5 {
		t.Fatalf("unexpected review response: code=%d stub=%+v body=%s", review.Code, stub, review.Body.String())
	}
}

func TestJudgeCalibrationHandlerRejectsMalformedReview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := new(judgeCalibrationServiceStub)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("userName", "alice")
	context.Request = httptest.NewRequest(http.MethodPost, "/evaluations/judge-calibration/reviews", bytes.NewBufferString(`{"case_id":"judge-cal-001","scores":{}} trailing`))
	context.Request.Header.Set("Content-Type", "application/json")
	NewJudgeCalibrationHandler(stub).Review(context)
	if stub.caseID != "" {
		t.Fatal("malformed review reached service")
	}
}
