package agentrun

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"GopherAI/common/mysql"
	"GopherAI/internal/diagnostic"
	"GopherAI/internal/harness"
	"GopherAI/internal/incident"
	"GopherAI/internal/observability"
	"GopherAI/internal/profilememory"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const responseSchemaVersion = "agent-run-api-v1"

type Handler struct {
	workflow    Workflow
	resolutions ResolutionService
}

type Workflow interface {
	Start(context.Context, diagnostic.StartCommand) (diagnostic.RunResponse, error)
	Get(context.Context, string, string) (diagnostic.RunResponse, error)
	Resume(context.Context, diagnostic.ResumeCommand) (diagnostic.RunResponse, error)
	Cancel(context.Context, string, string) (diagnostic.RunResponse, error)
}

type ResolutionService interface {
	Preview(context.Context, string, string, string) (incident.Proposal, error)
	Confirm(context.Context, incident.ConfirmCommand) (incident.Confirmation, error)
	Get(context.Context, string, string) (*incident.PublicResolvedIncident, error)
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

type ResolutionProposalRequest struct {
	HypothesisID string `json:"hypothesis_id" binding:"required"`
}

type ResolutionConfirmationRequest struct {
	HypothesisID         string `json:"hypothesis_id" binding:"required"`
	Resolution           string `json:"resolution" binding:"required"`
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

func NewHandler(workflow Workflow) *Handler { return &Handler{workflow: workflow} }

func NewHandlerWithResolutions(workflow Workflow, resolutions ResolutionService) *Handler {
	return &Handler{workflow: workflow, resolutions: resolutions}
}

func NewDefaultHandler() *Handler {
	lifecycle, err := harness.NewObservedService(harness.NewGormRepository(mysql.DB), harness.SystemClock{}, harness.UUIDGenerator{}, observability.DefaultMetrics())
	if err != nil {
		panic(err)
	}
	workflow, err := diagnostic.NewWorkflow(lifecycle, diagnostic.NewAgent())
	if err != nil {
		panic(err)
	}
	incidentRepository := incident.NewGormRepository(mysql.DB)
	profileService, err := profilememory.NewService(profilememory.NewGormRepository(mysql.DB), profilememory.SystemClock{})
	if err != nil {
		panic(err)
	}
	workflow.WithCaseRetriever(incidentRepository).WithCaseRecallObserver(observability.DefaultMetrics()).WithProfileMemory(profileService)
	resolutions, err := incident.NewService(workflow, incidentRepository, incident.SystemClock{})
	if err != nil {
		panic(err)
	}
	return NewHandlerWithResolutions(workflow, resolutions)
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
	userID := context.GetString("userName")
	response, err := handler.workflow.Resume(context.Request.Context(), diagnostic.ResumeCommand{
		RunID: context.Param("run_id"), TenantID: userID, UserID: userID, ClientRequestID: request.ClientRequestID,
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

func (handler *Handler) PreviewResolution(context *gin.Context) {
	if handler.resolutions == nil {
		handler.writeError(context, errors.New("resolution service is required"))
		return
	}
	request := new(ResolutionProposalRequest)
	if err := context.ShouldBindJSON(request); err != nil {
		handler.writeError(context, err)
		return
	}
	proposal, err := handler.resolutions.Preview(context.Request.Context(), context.GetString("userName"), context.Param("run_id"), request.HypothesisID)
	if err != nil {
		handler.writeError(context, err)
		return
	}
	context.JSON(http.StatusOK, proposal)
}

func (handler *Handler) ConfirmResolution(context *gin.Context) {
	if handler.resolutions == nil {
		handler.writeError(context, errors.New("resolution service is required"))
		return
	}
	request := new(ResolutionConfirmationRequest)
	if err := context.ShouldBindJSON(request); err != nil {
		handler.writeError(context, err)
		return
	}
	confirmation, err := handler.resolutions.Confirm(context.Request.Context(), incident.ConfirmCommand{
		RunID: context.Param("run_id"), UserID: context.GetString("userName"), HypothesisID: request.HypothesisID,
		Resolution: request.Resolution, ClientRequestID: request.ClientRequestID, ExpectedStateVersion: request.ExpectedStateVersion,
	})
	if err != nil {
		handler.writeError(context, err)
		return
	}
	status := http.StatusOK
	if confirmation.Created {
		status = http.StatusCreated
	}
	context.JSON(status, confirmation)
}

func (handler *Handler) GetResolution(context *gin.Context) {
	if handler.resolutions == nil {
		handler.writeError(context, errors.New("resolution service is required"))
		return
	}
	resolved, err := handler.resolutions.Get(context.Request.Context(), context.GetString("userName"), context.Param("run_id"))
	if err != nil {
		handler.writeError(context, err)
		return
	}
	if resolved == nil {
		context.JSON(http.StatusNotFound, ErrorResponse{SchemaVersion: responseSchemaVersion, Code: "RESOLUTION_NOT_FOUND", Message: "该诊断运行尚未确认解决方案", Retryable: false})
		return
	}
	context.JSON(http.StatusOK, resolved)
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
	case errors.Is(err, incident.ErrRunNotEligible):
		status, response.Code, response.Message, response.Retryable = http.StatusConflict, "RUN_NOT_RESOLUTION_ELIGIBLE", "只有成功结束且仍为待验证假设的诊断运行可以确认解决方案", false
	case errors.Is(err, incident.ErrHypothesisNotFound):
		status, response.Code, response.Message, response.Retryable = http.StatusNotFound, "HYPOTHESIS_NOT_FOUND", "未找到选中的诊断假设", false
	case errors.Is(err, incident.ErrInvalidConfirmation):
		status, response.Code, response.Message, response.Retryable = http.StatusBadRequest, "RESOLUTION_CONFIRMATION_INVALID", "请填写至少 5 个字符的实际解决办法", false
	case errors.Is(err, incident.ErrIdempotencyConflict):
		status, response.Code, response.Message, response.Retryable = http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "同一确认请求标识不能用于不同内容", false
	case errors.Is(err, incident.ErrAlreadyConfirmed):
		status, response.Code, response.Message, response.Retryable = http.StatusConflict, "RESOLUTION_ALREADY_CONFIRMED", "该诊断运行已经确认过不同的解决方案", false
	default:
		if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "bound") || strings.Contains(err.Error(), "timeout") {
			status, response.Code, response.Message, response.Retryable = http.StatusBadRequest, "INVALID_AGENT_RUN_REQUEST", "诊断请求参数不正确", false
		}
	}
	context.JSON(status, response)
}
