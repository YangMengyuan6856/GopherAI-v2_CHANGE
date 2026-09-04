package toolruntimecontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(requestid.Attach(), func(ctx *gin.Context) { ctx.Set("userName", "alice"); ctx.Next() })
	handler := NewHandler(service)
	router.GET("/api/v1/tools", handler.Catalog)
	router.POST("/api/v1/tools/invoke", handler.Invoke)
	router.POST("/api/v1/tools/agent", handler.RunAgent)
	return router
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
