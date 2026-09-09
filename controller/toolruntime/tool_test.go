package toolruntimecontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"GopherAI/internal/toolagent"
	toolruntime "GopherAI/internal/toolruntime"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

type fakeService struct {
	definitions []toolruntime.Definition
	message     toolruntime.ToolMessage
	invocation  toolruntime.Invocation
	calls       int
}

func (service *fakeService) Definitions() []toolruntime.Definition { return service.definitions }
func (service *fakeService) Invoke(_ context.Context, invocation toolruntime.Invocation) toolruntime.ToolMessage {
	service.invocation = invocation
	service.calls++
	return service.message
}

func newTestRouter(service Service) *gin.Engine {
	return newTestRouterWithPlanner(service, nil)
}

func newTestRouterWithPlanner(service Service, planner toolagent.CandidatePlanner) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(requestid.Attach(), func(ctx *gin.Context) { ctx.Set("userName", "alice"); ctx.Next() })
	handler := NewHandlerWithPlanner(service, planner)
	router.GET("/api/v1/tools", handler.Catalog)
	router.POST("/api/v1/tools/invoke", handler.Invoke)
	router.POST("/api/v1/tools/agent", handler.RunAgent)
	return router
}

type scriptedPlanner struct {
	plan        toolagent.Plan
	repairs     []toolagent.PlannedCall
	repairCalls int
}

func (planner *scriptedPlanner) Plan(string) (toolagent.Plan, error) { return planner.plan, nil }
func (planner *scriptedPlanner) Repair(_ string, _ toolagent.PlannedCall, _ toolagent.RepairFeedback) (toolagent.PlannedCall, error) {
	if planner.repairCalls >= len(planner.repairs) {
		return toolagent.PlannedCall{}, toolagent.ErrRepairUnavailable
	}
	repaired := planner.repairs[planner.repairCalls]
	planner.repairCalls++
	return repaired, nil
}

type governedCandidateTool struct {
	calls int
}

func (tool *governedCandidateTool) Definition() toolruntime.Definition {
	return toolruntime.Definition{
		Name: "guarded_read", Version: "1.0.0", Description: "test candidate governance",
		InputSchema: toolruntime.InputSchema{Type: "object", Properties: map[string]toolruntime.PropertySchema{
			"format": {Type: "string", Enum: []string{"summary"}, MinLength: 7, MaxLength: 7},
		}, Required: []string{"format"}, AdditionalProperties: false},
		AllowedIntents: []string{"tool_task"}, RequiredPermission: "devsupport:tools:read", SideEffect: toolruntime.SideEffectReadOnly,
		TimeoutMS: 100, MaxResultBytes: 1024, Idempotent: true, RetryMaxAttempts: 1,
	}
}
func (tool *governedCandidateTool) Execute(context.Context, map[string]any) (toolruntime.Output, error) {
	tool.calls++
	return toolruntime.Output{Data: map[string]any{"ok": true}, EvidenceRefs: []string{"test-evidence:guarded-read"}}, nil
}

type countAuditor struct{ count int }

func (auditor *countAuditor) Record(context.Context, toolruntime.Invocation, toolruntime.ToolMessage) error {
	auditor.count++
	return nil
}

func newCandidateRuntime(t *testing.T) (*toolruntime.Runtime, *governedCandidateTool, *countAuditor) {
	t.Helper()
	registry := toolruntime.NewRegistry()
	tool := &governedCandidateTool{}
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	auditor := &countAuditor{}
	runtime, err := toolruntime.NewRuntime(registry, auditor, nil)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, tool, auditor
}

func TestToolAgentExecutesBoundedCompoundPlanThroughRuntime(t *testing.T) {
	service := &fakeService{message: toolruntime.ToolMessage{ToolName: "test", Status: toolruntime.StatusSuccess}}
	router := newTestRouter(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tools/agent", bytes.NewBufferString(`{"message":"给出当前发布清单，并检查后端和 Worker 健康状态"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.calls != 2 {
		t.Fatalf("unexpected response %d calls=%d body=%s", response.Code, service.calls, response.Body.String())
	}
	var body AgentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "succeeded" || len(body.Plan.Calls) != 2 || body.Plan.Calls[0].ToolName != "deployment_manifest_lookup" || body.Plan.Calls[1].ToolName != "service_health_snapshot" {
		t.Fatalf("unexpected bounded run: %+v", body)
	}
	if service.invocation.Strategy != "tool_agent_v1" || service.invocation.Budget.MaxCalls != 2 || service.invocation.Budget.UsedCalls != 1 {
		t.Fatalf("unexpected runtime policy: %+v", service.invocation)
	}
}

func TestToolAgentRefusesUnsafeActionWithoutRuntimeCall(t *testing.T) {
	service := &fakeService{}
	router := newTestRouter(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tools/agent", bytes.NewBufferString(`{"message":"请重启后端并删除旧日志"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.calls != 0 || !bytes.Contains(response.Body.Bytes(), []byte(`"decision":"refuse"`)) {
		t.Fatalf("unsafe action was not refused: %d %s", response.Code, response.Body.String())
	}
}

func TestToolAgentRepairsArgumentsAtMostTwiceThroughRealRuntime(t *testing.T) {
	runtime, tool, auditor := newCandidateRuntime(t)
	planner := &scriptedPlanner{
		plan: toolagent.Plan{SchemaVersion: toolagent.SchemaVersion, PlannerVersion: "scripted-candidate", Decision: "execute", ReasonCode: "TEST", Calls: []toolagent.PlannedCall{
			{ToolName: "guarded_read", Arguments: json.RawMessage(`{"format":7}`), ReasonCode: "TEST"},
		}},
		repairs: []toolagent.PlannedCall{
			{ToolName: "guarded_read", Arguments: json.RawMessage(`{"format":"invalid"}`), ReasonCode: "REPAIR_1"},
			{ToolName: "guarded_read", Arguments: json.RawMessage(`{"format":"summary"}`), ReasonCode: "REPAIR_2"},
		},
	}
	router := newTestRouterWithPlanner(runtime, planner)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tools/agent", bytes.NewBufferString(`{"message":"repair candidate"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var body AgentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || body.Status != "succeeded" || body.RepairCount != 2 || len(body.Repairs) != 2 || len(body.AttemptMessages) != 3 {
		t.Fatalf("unexpected repaired run: code=%d body=%+v", response.Code, body)
	}
	if tool.calls != 1 || auditor.count != 3 || planner.repairCalls != 2 || body.ToolMessages[0].Status != toolruntime.StatusSuccess || string(body.Plan.Calls[0].Arguments) != `{"format":"summary"}` {
		t.Fatalf("repair bypassed governance: calls=%d audits=%d body=%+v", tool.calls, auditor.count, body)
	}
}

func TestToolAgentHardStopsAfterTwoRejectedRepairs(t *testing.T) {
	runtime, tool, auditor := newCandidateRuntime(t)
	invalid := toolagent.PlannedCall{ToolName: "guarded_read", Arguments: json.RawMessage(`{"format":7}`), ReasonCode: "INVALID"}
	planner := &scriptedPlanner{
		plan: toolagent.Plan{SchemaVersion: toolagent.SchemaVersion, PlannerVersion: "scripted-candidate", Decision: "execute", Calls: []toolagent.PlannedCall{invalid}},
		repairs: []toolagent.PlannedCall{
			{ToolName: "guarded_read", Arguments: json.RawMessage(`{"format":"bad-one"}`)},
			{ToolName: "guarded_read", Arguments: json.RawMessage(`{"format":"bad-two"}`)},
			{ToolName: "guarded_read", Arguments: json.RawMessage(`{"format":"summary"}`)},
		},
	}
	router := newTestRouterWithPlanner(runtime, planner)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tools/agent", bytes.NewBufferString(`{"message":"bounded repair"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var body AgentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "failed" || body.TerminationReason != "SCHEMA_REPAIR_LIMIT_REACHED" || body.RepairCount != 2 || planner.repairCalls != 2 || len(body.AttemptMessages) != 3 {
		t.Fatalf("repair limit was not enforced: %+v", body)
	}
	if tool.calls != 0 || auditor.count != 3 {
		t.Fatalf("invalid repair executed or escaped audit: calls=%d audits=%d", tool.calls, auditor.count)
	}
}

func TestToolAgentNeverRepairsOrFuzzyMatchesUnknownName(t *testing.T) {
	runtime, tool, auditor := newCandidateRuntime(t)
	planner := &scriptedPlanner{plan: toolagent.Plan{SchemaVersion: toolagent.SchemaVersion, PlannerVersion: "scripted-candidate", Decision: "execute", Calls: []toolagent.PlannedCall{
		{ToolName: "guarded_rea", Arguments: json.RawMessage(`{"format":"summary"}`)},
	}}, repairs: []toolagent.PlannedCall{{ToolName: "guarded_read", Arguments: json.RawMessage(`{"format":"summary"}`)}}}
	router := newTestRouterWithPlanner(runtime, planner)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tools/agent", bytes.NewBufferString(`{"message":"wrong tool name"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var body AgentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "failed" || body.TerminationReason != "UNKNOWN_TOOL_REJECTED" || body.ToolMessages[0].ErrorCode != toolruntime.ErrorToolNotRegistered || planner.repairCalls != 0 || tool.calls != 0 || auditor.count != 1 {
		t.Fatalf("unknown tool was guessed or executed: %+v", body)
	}
}

func TestToolAgentExecutionBoundaryTruncatesUntrustedCandidatePlan(t *testing.T) {
	service := &fakeService{message: toolruntime.ToolMessage{ToolName: "test", Status: toolruntime.StatusSuccess}}
	calls := []toolagent.PlannedCall{
		{ToolName: "one", Arguments: json.RawMessage(`{}`)},
		{ToolName: "two", Arguments: json.RawMessage(`{}`)},
		{ToolName: "three", Arguments: json.RawMessage(`{}`)},
	}
	planner := &scriptedPlanner{plan: toolagent.Plan{SchemaVersion: toolagent.SchemaVersion, PlannerVersion: "untrusted-candidate", Decision: "execute", Calls: calls}}
	router := newTestRouterWithPlanner(service, planner)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tools/agent", bytes.NewBufferString(`{"message":"oversized candidate plan"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var body AgentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "succeeded" || service.calls != toolagent.MaxPlanCalls || len(body.Plan.Calls) != toolagent.MaxPlanCalls || body.Plan.OmittedCount != 1 || service.invocation.Budget.MaxCalls != toolagent.MaxPlanCalls {
		t.Fatalf("candidate plan boundary failed: calls=%d body=%+v", service.calls, body)
	}
}

func TestToolAgentTerminatesDuplicateActionAsNoProgress(t *testing.T) {
	runtime, tool, auditor := newCandidateRuntime(t)
	call := toolagent.PlannedCall{ToolName: "guarded_read", Arguments: json.RawMessage(`{"format":"summary"}`), ReasonCode: "TEST"}
	planner := &scriptedPlanner{plan: toolagent.Plan{SchemaVersion: toolagent.SchemaVersion, PlannerVersion: "scripted-candidate", Decision: "execute", Calls: []toolagent.PlannedCall{call, call}}}
	router := newTestRouterWithPlanner(runtime, planner)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tools/agent", bytes.NewBufferString(`{"message":"repeat action"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var body AgentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "partial_failed" || body.TerminationReason != "NO_PROGRESS" || len(body.ToolMessages) != 2 || body.ToolMessages[1].ErrorCode != toolruntime.ErrorNoProgress {
		t.Fatalf("duplicate action did not terminate: %+v", body)
	}
	if tool.calls != 1 || auditor.count != 2 {
		t.Fatalf("duplicate action executed: calls=%d audits=%d", tool.calls, auditor.count)
	}
}

func TestInvokeBuildsServerOwnedGovernanceContext(t *testing.T) {
	service := &fakeService{message: toolruntime.ToolMessage{CallID: "request-1", ToolName: "deployment_manifest_lookup", Status: toolruntime.StatusSuccess}}
	router := newTestRouter(service)
	body := []byte(`{"tool_name":"deployment_manifest_lookup","arguments":{}}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tools/invoke", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.calls != 1 {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	call := service.invocation
	if call.Principal.UserID != "alice" || !call.Principal.Permissions["devsupport:tools:read"] || call.AllowedSideEffect != toolruntime.SideEffectReadOnly || call.TraceID == "" {
		t.Fatalf("governance context was not server-owned: %+v", call)
	}
	if call.Intent != "tool_task" || call.Strategy != "tool_primary" || call.Budget.MaxCalls != 1 {
		t.Fatalf("unexpected execution policy: %+v", call)
	}
}

func TestInvokeRejectsUnknownTopLevelFields(t *testing.T) {
	service := &fakeService{}
	router := newTestRouter(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tools/invoke", bytes.NewBufferString(`{"tool_name":"deployment_manifest_lookup","arguments":{},"permission":"admin"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("unsafe top-level field was accepted: %d %s", response.Code, response.Body.String())
	}
}

func TestCatalogReturnsVersionedDefinitions(t *testing.T) {
	definition := toolruntime.NewDeploymentManifestTool("fixed").Definition()
	service := &fakeService{definitions: []toolruntime.Definition{definition}}
	router := newTestRouter(service)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("catalog failed: %d", response.Code)
	}
	var body CatalogResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SchemaVersion != toolruntime.SchemaVersion || len(body.Tools) != 1 || body.Tools[0].Name != "deployment_manifest_lookup" {
		t.Fatalf("unexpected catalog: %+v", body)
	}
}

func TestPublicToolCatalogNeverExposesResolutionWriteAdapter(t *testing.T) {
	handler := NewDefaultHandler()
	foundOfficialDocumentTool := false
	for _, definition := range handler.runtime.Definitions() {
		if definition.Name == "confirm_resolution" || definition.SideEffect != toolruntime.SideEffectReadOnly {
			t.Fatalf("internal write adapter leaked into public tool catalog: %+v", definition)
		}
		if definition.Name == "official_document_search" {
			foundOfficialDocumentTool = true
		}
	}
	if !foundOfficialDocumentTool {
		t.Fatal("official document evidence tool is missing from governed catalog")
	}
}
