package evaluation

import (
	"GopherAI/internal/failurepool"
	"GopherAI/model"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type failurePoolServiceStub struct{ audit failurepool.Audit }

func (stub failurePoolServiceStub) Audit(context.Context) (failurepool.Audit, error) {
	return stub.audit, nil
}
func (stub failurePoolServiceStub) RunCycle(context.Context) (failurepool.CycleResult, error) {
	return failurepool.CycleResult{Audit: stub.audit, Created: true}, nil
}

func TestFailurePoolEndpointExposesMetadataButNoRawSampleContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	run := model.FailureMiningRun{ID: strings.Repeat("a", 64), InputHash: strings.Repeat("b", 64), SampleCount: 2, EligibleCount: 1, ClusterCount: 1, ProposalCount: 1, CreatedAt: now}
	audit := failurepool.Audit{
		SchemaVersion: failurepool.SchemaVersion, Mode: "slow_loop_shadow", HasRun: true, Run: &run,
		Clusters:  []model.FailureCluster{{ID: strings.Repeat("c", 64), RunID: run.ID, WhereCode: "answer_quality", WhyCode: "user_rejected_answer", PrimaryReason: "user_downvote", SampleCount: 1}},
		Proposals: []failurepool.PublicProposal{{ID: strings.Repeat("d", 64), ClusterID: strings.Repeat("c", 64), CandidateKind: "dataset", Target: "user_rejected_answer_case", State: "pending_human_review", RequiresHumanReview: true}},
	}
	handler := NewFailurePoolHandler(failurePoolServiceStub{audit: audit}, func() time.Time { return now })
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/evaluations/failure-pool/latest", nil)
	handler.Latest(ginContext)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"where_code":"answer_quality"`) || !strings.Contains(recorder.Body.String(), `"applied":false`) {
		t.Fatalf("unexpected failure pool response: %d %s", recorder.Code, recorder.Body.String())
	}
	for _, forbidden := range []string{`"question":`, `"answer":`, `"evidence_json":`, `"user_hash":`, `"request_hash":`, `"change_json":`} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("failure pool API exposed %s: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestFailurePoolAcceptanceIsDeterministicAndNonMutating(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/evaluations/failure-pool/acceptance", nil)
	NewFailurePoolHandler(nil, func() time.Time { return time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC) }).Acceptance(ginContext)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"passed":true`) || !strings.Contains(recorder.Body.String(), `"eligible_samples":8`) || !strings.Contains(recorder.Body.String(), `"active_policy_write":false`) {
		t.Fatalf("unexpected acceptance report: %d %s", recorder.Code, recorder.Body.String())
	}
}
