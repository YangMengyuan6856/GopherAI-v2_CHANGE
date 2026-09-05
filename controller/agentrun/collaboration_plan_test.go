package agentrun

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/orchestration"

	"github.com/gin-gonic/gin"
)

type fakeCollaborationPlanner struct {
	plan  orchestration.CollaborationPlan
	err   error
	calls int
}

func (planner *fakeCollaborationPlanner) Plan(_ context.Context, _ string) (orchestration.CollaborationPlan, error) {
	planner.calls++
	return planner.plan, planner.err
}

type fakeCollaborationObserver struct {
	decision string
	reason   string
	calls    int
}

func (observer *fakeCollaborationObserver) RecordCollaborationPlan(decision string, reason string, _ time.Duration) {
	observer.calls++
	observer.decision, observer.reason = decision, reason
}

func collaborationPlanRouter(planner CollaborationPlanner, observer CollaborationPlanObserver, principal string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(ctx *gin.Context) { ctx.Set("userName", principal); ctx.Next() })
	engine.POST("/plan", NewCollaborationPlanHandler(planner, observer).Plan)
	return engine
}

func TestCollaborationPlanHandlerReturnsShadowBoundary(t *testing.T) {
	planner := &fakeCollaborationPlanner{plan: orchestration.CollaborationPlan{
		SchemaVersion: orchestration.PlanSchemaVersion, PlannerVersion: orchestration.PlannerVersion,
		Mode: "shadow_only", AffectsLiveTraffic: false, Decision: orchestration.DecisionCollaborative,
		ReasonCode: "knowledge_diagnostic_split", Tasks: []orchestration.PlannedTask{},
	}}
	observer := new(fakeCollaborationObserver)
	engine := collaborationPlanRouter(planner, observer, "alice")
	request := httptest.NewRequest(http.MethodPost, "/plan", bytes.NewBufferString(`{"message":"HTTP 502，同时核对项目文档"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || planner.calls != 1 || observer.calls != 1 || observer.decision != orchestration.DecisionCollaborative {
		t.Fatalf("collaboration plan request failed: code=%d observer=%+v body=%s", response.Code, observer, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"mode":"shadow_only"`) || !strings.Contains(response.Body.String(), `"affects_live_traffic":false`) {
		t.Fatalf("shadow boundary is absent: %s", response.Body.String())
	}
}

func TestCollaborationPlanHandlerRejectsUnknownFields(t *testing.T) {
	planner := new(fakeCollaborationPlanner)
	observer := new(fakeCollaborationObserver)
	engine := collaborationPlanRouter(planner, observer, "alice")
	request := httptest.NewRequest(http.MethodPost, "/plan", bytes.NewBufferString(`{"message":"Redis NOAUTH","max_agents":99}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || planner.calls != 0 || observer.decision != "error" {
		t.Fatalf("caller-controlled budget reached planner: code=%d calls=%d body=%s", response.Code, planner.calls, response.Body.String())
	}
}

func TestCollaborationPlanHandlerRequiresPrincipal(t *testing.T) {
	planner := new(fakeCollaborationPlanner)
	engine := collaborationPlanRouter(planner, nil, "")
	request := httptest.NewRequest(http.MethodPost, "/plan", bytes.NewBufferString(`{"message":"Redis NOAUTH"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || planner.calls != 0 {
		t.Fatalf("anonymous caller reached planner: code=%d calls=%d", response.Code, planner.calls)
	}
}
