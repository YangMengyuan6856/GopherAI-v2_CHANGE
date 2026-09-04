package toolruntimecontroller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"GopherAI/common/mysql"
	"GopherAI/internal/observability"
	"GopherAI/internal/toolagent"
	toolruntime "GopherAI/internal/toolruntime"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const maximumInvokeBodyBytes = 32 * 1024

type Service interface {
	Definitions() []toolruntime.Definition
	Invoke(context.Context, toolruntime.Invocation) toolruntime.ToolMessage
}

type Handler struct {
	runtime Service
	planner toolagent.CandidatePlanner
}

type InvokeRequest struct {
	ToolName  string          `json:"tool_name"`
	Arguments json.RawMessage `json:"arguments"`
	Intent    string          `json:"intent,omitempty"`
}

type CatalogResponse struct {
	SchemaVersion string                   `json:"schema_version"`
	Tools         []toolruntime.Definition `json:"tools"`
}

type AgentRequest struct {
	Message string `json:"message"`
}

type AgentResponse struct {
	SchemaVersion     string                    `json:"schema_version"`
	Status            string                    `json:"status"`
	Plan              toolagent.Plan            `json:"plan"`
	ToolMessages      []toolruntime.ToolMessage `json:"tool_messages"`
	AttemptMessages   []toolruntime.ToolMessage `json:"attempt_messages,omitempty"`
	Repairs           []toolagent.RepairRecord  `json:"repairs,omitempty"`
	RepairCount       int                       `json:"repair_count"`
	TerminationReason string                    `json:"termination_reason,omitempty"`
	CachedCount       int                       `json:"cached_count"`
}

type ErrorResponse struct {
	SchemaVersion string `json:"schema_version"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Retryable     bool   `json:"retryable"`
	TraceID       string `json:"trace_id,omitempty"`
}

func NewHandler(runtime Service) *Handler {
	return NewHandlerWithPlanner(runtime, toolagent.NewPlanner())
}

func NewHandlerWithPlanner(runtime Service, planner toolagent.CandidatePlanner) *Handler {
	if planner == nil {
		planner = toolagent.NewPlanner()
	}
	return &Handler{runtime: runtime, planner: planner}
}

func NewDefaultHandler() *Handler {
	registry := toolruntime.NewRegistry()
	if err := registry.Register(toolruntime.NewDeploymentManifestTool("release-manifest.json")); err != nil {
		panic(err)
	}
	if err := registry.Register(toolruntime.NewServiceHealthTool()); err != nil {
		panic(err)
	}
	if err := registry.Register(toolruntime.NewBoundedLogSignatureTool(".")); err != nil {
		panic(err)
	}
	if err := registry.Register(toolruntime.NewMCPDeploymentEvidenceTool("http://127.0.0.1:8081/mcp")); err != nil {
		panic(err)
	}
	if err := registry.Register(toolruntime.NewOfficialDocumentSearchTool()); err != nil {
		panic(err)
	}
	runtime, err := toolruntime.NewRuntime(registry, toolruntime.NewGormAuditor(mysql.DB), observability.DefaultMetrics())
	if err != nil {
		panic(err)
	}
	return NewHandler(runtime)
}

func (handler *Handler) Catalog(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, CatalogResponse{SchemaVersion: toolruntime.SchemaVersion, Tools: handler.runtime.Definitions()})
}

func (handler *Handler) Invoke(ctx *gin.Context) {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maximumInvokeBodyBytes)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	request := new(InvokeRequest)
	if err := decoder.Decode(request); err != nil {
		handler.writeError(ctx, http.StatusBadRequest, "TOOL_REQUEST_INVALID", "工具调用请求必须是合法且字段受限的 JSON", false)
		return
	}
	if err := ensureRequestEOF(decoder); err != nil {
		handler.writeError(ctx, http.StatusBadRequest, "TOOL_REQUEST_INVALID", "工具调用请求只能包含一个 JSON 对象", false)
		return
	}
	request.ToolName = strings.TrimSpace(request.ToolName)
	if request.ToolName == "" || len(request.ToolName) > 64 {
		handler.writeError(ctx, http.StatusBadRequest, "TOOL_NAME_INVALID", "请提供合法的精确工具名称", false)
		return
	}
	if len(request.Arguments) == 0 {
		request.Arguments = json.RawMessage(`{}`)
	}
	request.Intent = strings.TrimSpace(request.Intent)
	if request.Intent == "" {
		request.Intent = "tool_task"
	}
	requestID, traceID := requestid.IDs(ctx)
	userID := ctx.GetString("userName")
	message := handler.runtime.Invoke(ctx.Request.Context(), toolruntime.Invocation{
		CallID: requestID, TraceID: traceID, ToolName: request.ToolName, Arguments: request.Arguments, Intent: request.Intent, Strategy: "tool_primary",
		Principal:         toolruntime.Principal{TenantID: userID, UserID: userID, Permissions: map[string]bool{"devsupport:tools:read": true}},
		AllowedSideEffect: toolruntime.SideEffectReadOnly, Budget: toolruntime.CallBudget{MaxCalls: 1},
	})
	ctx.JSON(statusForMessage(message), message)
}

func (handler *Handler) RunAgent(ctx *gin.Context) {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maximumInvokeBodyBytes)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	request := new(AgentRequest)
	if err := decoder.Decode(request); err != nil {
		handler.writeError(ctx, http.StatusBadRequest, "TOOL_AGENT_REQUEST_INVALID", "工具 Agent 请求必须是合法且字段受限的 JSON", false)
		return
	}
	if err := ensureRequestEOF(decoder); err != nil {
		handler.writeError(ctx, http.StatusBadRequest, "TOOL_AGENT_REQUEST_INVALID", "工具 Agent 请求只能包含一个 JSON 对象", false)
		return
	}
	plan, err := handler.planner.Plan(request.Message)
	if err != nil {
		handler.writeError(ctx, http.StatusBadRequest, "TOOL_AGENT_INPUT_INVALID", "请输入 1 到 2000 个字符的运维证据问题", false)
		return
	}
	response := AgentResponse{SchemaVersion: "tool-agent-run-v1", Status: plan.Decision, Plan: plan, ToolMessages: []toolruntime.ToolMessage{}, AttemptMessages: []toolruntime.ToolMessage{}, Repairs: []toolagent.RepairRecord{}}
	if plan.Decision != "execute" {
		ctx.JSON(http.StatusOK, response)
		return
	}
	requestID, traceID := requestid.IDs(ctx)
	userID := ctx.GetString("userName")
	execution := toolagent.ExecuteCandidatePlan(ctx.Request.Context(), handler.runtime, handler.planner, toolagent.ExecutionRequest{
		Message: request.Message, Plan: plan, CallIDPrefix: requestID, TraceID: traceID, Strategy: "tool_agent_v1",
		Principal:     toolruntime.Principal{TenantID: userID, UserID: userID, Permissions: map[string]bool{"devsupport:tools:read": true}},
		AllowedEffect: toolruntime.SideEffectReadOnly,
	})
	response.Status, response.Plan = execution.Status, execution.Plan
	response.ToolMessages, response.AttemptMessages = execution.ToolMessages, execution.AttemptMessages
	response.Repairs, response.RepairCount = execution.Repairs, execution.RepairCount
	response.TerminationReason, response.CachedCount = execution.TerminationReason, execution.CachedCount
	ctx.JSON(http.StatusOK, response)
}

func statusForMessage(message toolruntime.ToolMessage) int {
	switch message.ErrorCode {
	case "":
		return http.StatusOK
	case toolruntime.ErrorToolNotRegistered:
		return http.StatusNotFound
	case toolruntime.ErrorIntentDenied, toolruntime.ErrorPermissionDenied, toolruntime.ErrorSideEffectDenied:
		return http.StatusForbidden
	case toolruntime.ErrorArgumentsInvalid, toolruntime.ErrorBudgetExceeded:
		return http.StatusBadRequest
	case toolruntime.ErrorNoProgress:
		return http.StatusConflict
	case toolruntime.ErrorTimeout:
		return http.StatusGatewayTimeout
	case toolruntime.ErrorCancelled:
		return 499
	default:
		return http.StatusServiceUnavailable
	}
}

func (handler *Handler) writeError(ctx *gin.Context, status int, code string, message string, retryable bool) {
	_, traceID := requestid.IDs(ctx)
	ctx.JSON(status, ErrorResponse{SchemaVersion: toolruntime.SchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}

func ensureRequestEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}
