package agentrun

import (
	"errors"
	"net/http"
	"strings"

	"GopherAI/common/mysql"
	"GopherAI/internal/diagnostic"
	"GopherAI/internal/harness"
	"GopherAI/internal/observability"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const responseSchemaVersion = "agent-run-api-v1"

type Handler struct {
	workflow *diagnostic.Workflow
}

type StartRequest struct {
	Message         string `json:"message" binding:"required"`
	SessionID       string `json:"session_id,omitempty"`
	ClientRequestID string `json:"client_request_id,omitempty"`
}

type ResumeRequest struct {
	Message              string `json:"message" binding:"required"`
	ClientRequestID      string `json:"client_request_id" binding:"required"`
	ExpectedStateVersion int64  `json:"expected_state_version" binding:"required"`
}

type PublicCheckpoint struct {
	Goal           string            `json:"goal"`
	ConfirmedFacts map[string]string `json:"confirmed_facts,omitempty"`
	OpenQuestions  []string          `json:"open_questions,omitempty"`
	EvidenceRefs   []string          `json:"evidence_refs,omitempty"`
	NextAction     string            `json:"next_action,omitempty"`
}

type Response struct {
	SchemaVersion string               `json:"schema_version"`
	Created       bool                 `json:"created"`
	Run           harness.Run          `json:"run"`
	Steps         []harness.PublicStep `json:"steps"`
	Checkpoint    *PublicCheckpoint    `json:"checkpoint,omitempty"`
	Result        *diagnostic.Result   `json:"result,omitempty"`
}

type ErrorResponse struct {
	SchemaVersion string `json:"schema_version"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Retryable     bool   `json:"retryable"`
	TraceID       string `json:"trace_id,omitempty"`
}

func NewHandler(workflow *diagnostic.Workflow) *Handler { return &Handler{workflow: workflow} }

func NewDefaultHandler() *Handler {
	lifecycle, err := harness.NewObservedService(harness.NewGormRepository(mysql.DB), harness.SystemClock{}, harness.UUIDGenerator{}, observability.DefaultMetrics())
	if err != nil {
		panic(err)
	}
	workflow, err := diagnostic.NewWorkflow(lifecycle, diagnostic.NewAgent())
	if err != nil {
		panic(err)
	}
	return NewHandler(workflow)
}

func (handler *Handler) Start(context *gin.Context) {
	var request StartRequest
	if err := context.ShouldBindJSON(&request); err != nil {
		handler.writeError(context, err)
		return
	}
	requestID, traceID := requestid.IDs(context)
	clientRequestID := strings.TrimSpace(request.ClientRequestID)
	if clientRequestID == "" {
		clientRequestID = requestID
	}
	userID := context.GetString("userName")
	response, err := handler.workflow.Start(context.Request.Context(), diagnostic.StartCommand{
		TenantID: userID, UserID: userID, ClientRequestID: clientRequestID, RequestID: requestID, TraceID: traceID,
		SessionID: strings.TrimSpace(request.SessionID), Message: request.Message,
	})
	if err != nil {
		handler.writeError(context, err)
		return
	}
	status := http.StatusOK
	if response.Created {
		status = http.StatusCreated
	}
	context.JSON(status, publicResponse(response))
}

func (handler *Handler) Get(context *gin.Context) {
	response, err := handler.workflow.Get(context.Request.Context(), context.Param("run_id"), context.GetString("userName"))
	if err != nil {
		handler.writeError(context, err)
		return
	}
	context.JSON(http.StatusOK, publicResponse(response))
}

func (handler *Handler) Resume(context *gin.Context) {
	var request ResumeRequest
	if err := context.ShouldBindJSON(&request); err != nil {
		handler.writeError(context, err)
		return
	}
	response, err := handler.workflow.Resume(context.Request.Context(), diagnostic.ResumeCommand{
		RunID: context.Param("run_id"), UserID: context.GetString("userName"), ClientRequestID: request.ClientRequestID,
		ExpectedVersion: request.ExpectedStateVersion, Message: request.Message,
	})
	if err != nil {
		handler.writeError(context, err)
		return
	}
	context.JSON(http.StatusOK, publicResponse(response))
}

func (handler *Handler) Cancel(context *gin.Context) {
	response, err := handler.workflow.Cancel(context.Request.Context(), context.Param("run_id"), context.GetString("userName"))
	if err != nil {
		handler.writeError(context, err)
		return
	}
	context.JSON(http.StatusOK, publicResponse(response))
}

func publicResponse(response diagnostic.RunResponse) Response {
	result := Response{SchemaVersion: responseSchemaVersion, Created: response.Created, Run: response.Detail.Run, Steps: response.Detail.Steps, Result: response.Result}
	if response.Detail.Checkpoint != nil {
		result.Checkpoint = &PublicCheckpoint{
			Goal: response.Detail.Checkpoint.Goal, ConfirmedFacts: response.Detail.Checkpoint.ConfirmedFacts,
			OpenQuestions: response.Detail.Checkpoint.OpenQuestions, EvidenceRefs: response.Detail.Checkpoint.EvidenceRefs,
			NextAction: response.Detail.Checkpoint.NextAction,
		}
	}
	return result
}

func (handler *Handler) writeError(context *gin.Context, err error) {
	_, traceID := requestid.IDs(context)
	status := http.StatusInternalServerError
	response := ErrorResponse{SchemaVersion: responseSchemaVersion, Code: "AGENT_RUN_INTERNAL", Message: "诊断运行暂时不可用", Retryable: true, TraceID: traceID}
	switch {
	case errors.Is(err, diagnostic.ErrEmptyDiagnosticInput):
		status, response.Code, response.Message, response.Retryable = http.StatusBadRequest, "DIAGNOSTIC_INPUT_EMPTY", "请提供故障现象或日志", false
	case errors.Is(err, harness.ErrRunNotFound):
		status, response.Code, response.Message, response.Retryable = http.StatusNotFound, "AGENT_RUN_NOT_FOUND", "未找到该诊断运行", false
	case errors.Is(err, harness.ErrRunConflict):
		status, response.Code, response.Message, response.Retryable = http.StatusConflict, "RUN_STATE_CONFLICT", "运行状态已变化，请刷新后重试", true
	case errors.Is(err, harness.ErrBudgetExceeded):
		status, response.Code, response.Message, response.Retryable = http.StatusTooManyRequests, "AGENT_BUDGET_EXCEEDED", "诊断已达到执行预算", false
	case errors.Is(err, harness.ErrInvalidTransition):
		status, response.Code, response.Message, response.Retryable = http.StatusConflict, "INVALID_RUN_TRANSITION", "当前运行状态不允许该操作", false
	default:
		if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "bound") || strings.Contains(err.Error(), "timeout") {
			status, response.Code, response.Message, response.Retryable = http.StatusBadRequest, "INVALID_AGENT_RUN_REQUEST", "诊断请求参数不正确", false
		}
	}
	context.JSON(status, response)
}
