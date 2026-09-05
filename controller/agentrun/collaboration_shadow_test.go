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

type collaborationRunnerStub struct {
	result orchestration.CollaborationRun
	err    error
	calls  int
}

func (stub *collaborationRunnerStub) Run(context.Context, orchestration.ExecutionInput) (orchestration.CollaborationRun, error) {
	stub.calls++
	return stub.result, stub.err
}

type collaborationObserverStub struct {
	runs      int
	plans     int
	tasks     int
	synthesis int
}

func (stub *collaborationObserverStub) RecordCollaborationPlan(string, string, time.Duration) {
	stub.plans++
}
func (stub *collaborationObserverStub) RecordCollaborationRun(string, string, time.Duration) {
	stub.runs++
}
func (stub *collaborationObserverStub) RecordCollaborationTask(string, string, time.Duration) {
	stub.tasks++
}
func (stub *collaborationObserverStub) RecordCollaborationSynthesis(string, string) { stub.synthesis++ }

func collaborationShadowRouter(runner CollaborationShadowRunner, observer CollaborationShadowObserver, principal string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(ctx *gin.Context) { ctx.Set("userName", principal); ctx.Next() })
	engine.POST("/shadow", NewCollaborationShadowHandler(runner, observer).Run)
	return engine
}

func TestCollaborationShadowHandlerReturnsAuditableResult(t *testing.T) {
	runner := &collaborationRunnerStub{result: orchestration.CollaborationRun{
		SchemaVersion: orchestration.CollaborationRunSchemaVersion, Mode: "shadow_only", Executed: true, Status: "complete",
		Plan: orchestration.CollaborationPlan{Decision: orchestration.DecisionCollaborative, ReasonCode: "knowledge_diagnostic_split"},
		Execution: &orchestration.ExecutionResult{TaskResults: []orchestration.TaskExecution{
			{Agent: orchestration.KnowledgeAgentRole, Status: orchestration.TaskStatusSucceeded},
			{Agent: orchestration.DiagnosticAgentRole, Status: orchestration.TaskStatusSucceeded},
		}},
		Synthesis: &orchestration.SynthesisResult{Status: orchestration.SynthesisComplete, ReasonCode: "all_claims_citation_verified"},
	}}
	observer := new(collaborationObserverStub)
	engine := collaborationShadowRouter(runner, observer, "alice")
	request := httptest.NewRequest(http.MethodPost, "/shadow", bytes.NewBufferString(`{"message":"HTTP 502，同时核对项目文档"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || runner.calls != 1 || observer.runs != 1 || observer.plans != 1 || observer.tasks != 2 || observer.synthesis != 1 {
		t.Fatalf("shadow result was not observed: code=%d observer=%+v body=%s", response.Code, observer, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"mode":"shadow_only"`) || !strings.Contains(response.Body.String(), `"affects_live_traffic":false`) {
		t.Fatalf("shadow boundary is absent: %s", response.Body.String())
	}
}

func TestCollaborationShadowHandlerRejectsCallerControlledBudget(t *testing.T) {
	runner := new(collaborationRunnerStub)
	engine := collaborationShadowRouter(runner, nil, "alice")
	request := httptest.NewRequest(http.MethodPost, "/shadow", bytes.NewBufferString(`{"message":"Redis NOAUTH","max_agents":9}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || runner.calls != 0 {
		t.Fatalf("caller-controlled budget reached runner: code=%d calls=%d body=%s", response.Code, runner.calls, response.Body.String())
	}
}

func TestCollaborationShadowHandlerRequiresPrincipal(t *testing.T) {
	runner := new(collaborationRunnerStub)
	engine := collaborationShadowRouter(runner, nil, "")
	request := httptest.NewRequest(http.MethodPost, "/shadow", bytes.NewBufferString(`{"message":"Redis NOAUTH"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || runner.calls != 0 {
		t.Fatalf("anonymous request reached runner: code=%d calls=%d", response.Code, runner.calls)
	}
}
