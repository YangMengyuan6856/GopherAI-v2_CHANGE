package session

import (
	"GopherAI/internal/app"
	"GopherAI/internal/contract"
	"GopherAI/internal/observability"
	"GopherAI/internal/platform/feature"
	"GopherAI/internal/platform/legacy"
	"GopherAI/internal/policy"
	"GopherAI/middleware/requestid"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type ChatApplication interface {
	Chat(ctx context.Context, input app.ChatInput) (app.ChatOutput, error)
	Stream(ctx context.Context, input app.ChatInput, emit app.StreamEmitter) (app.ChatOutput, error)
}

type AutoHandler struct {
	application ChatApplication
	observer    ChatObserver
}

type ChatObserver interface {
	Record(output app.ChatOutput, requestError error)
}

type AutoChatRequest struct {
	Message         string `json:"message" binding:"required"`
	SessionID       string `json:"session_id,omitempty"`
	ClientRequestID string `json:"client_request_id,omitempty"`
	Debug           bool   `json:"debug"`
}

type AutoChatResponse struct {
	SchemaVersion string              `json:"schema_version"`
	TraceID       string              `json:"trace_id"`
	RequestID     string              `json:"request_id"`
	SessionID     string              `json:"session_id"`
	Message       string              `json:"message"`
	Intent        string              `json:"intent"`
	Strategy      string              `json:"strategy"`
	PolicyVersion string              `json:"policy_version"`
	Confidence    float64             `json:"confidence"`
	Citations     []contract.Citation `json:"citations,omitempty"`
}

func NewAutoHandler(application ChatApplication) *AutoHandler {
	return &AutoHandler{application: application}
}

func NewObservedAutoHandler(application ChatApplication, observer ChatObserver) *AutoHandler {
	return &AutoHandler{application: application, observer: observer}
}

func NewDefaultAutoHandler() *AutoHandler {
	flags := feature.DefaultProvider()
	selector := policy.NewFixedSelector(flags)
	application, err := app.NewService(selector, app.SystemClock{}, app.UUIDGenerator{}, legacy.NewDefaultChatStrategy())
	if err != nil {
		panic(fmt.Sprintf("initialize auto chat application: %v", err))
	}
	return NewObservedAutoHandler(application, observability.NewDefaultRecorder())
}

func (handler *AutoHandler) Chat(context *gin.Context) {
	request := new(AutoChatRequest)
	if err := context.ShouldBindJSON(request); err != nil {
		handler.writeJSONError(context, contract.NewDomainError("INVALID_CHAT_REQUEST", contract.ErrorValidation, "请求参数错误", false, err))
		return
	}
	output, err := handler.application.Chat(context.Request.Context(), chatInput(context, request))
	if err != nil {
		handler.record(output, err)
		handler.writeJSONError(context, err)
		return
	}
	setTraceHeaders(context, output)
	handler.record(output, nil)
	context.JSON(http.StatusOK, AutoChatResponse{
		SchemaVersion: contract.SchemaVersion,
		TraceID:       output.Request.TraceID,
		RequestID:     output.Request.RequestID,
		SessionID:     output.Result.SessionID,
		Message:       output.Result.Answer,
		Intent:        output.Intent.Intent,
		Strategy:      output.Decision.StrategyName,
		PolicyVersion: output.Decision.PolicyVersion,
		Confidence:    output.Result.Confidence,
		Citations:     output.Result.Citations,
	})
}

func (handler *AutoHandler) Stream(context *gin.Context) {
	request := new(AutoChatRequest)
	if err := context.ShouldBindJSON(request); err != nil {
		handler.writeJSONError(context, contract.NewDomainError("INVALID_CHAT_REQUEST", contract.ErrorValidation, "请求参数错误", false, err))
		return
	}
	flusher, ok := context.Writer.(http.Flusher)
	if !ok {
		handler.writeJSONError(context, contract.NewDomainError("STREAM_UNSUPPORTED", contract.ErrorInternal, "当前连接不支持流式输出", false, nil))
		return
	}
	context.Header("Content-Type", "text/event-stream; charset=utf-8")
	context.Header("Cache-Control", "no-cache")
	context.Header("Connection", "keep-alive")
	context.Header("X-Accel-Buffering", "no")

	output, err := handler.application.Stream(context.Request.Context(), chatInput(context, request), func(event contract.StreamEvent) error {
		if context.Request.Context().Err() != nil {
			return context.Request.Context().Err()
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(context.Writer, "event: %s\ndata: %s\n\n", event.Type, encoded); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err != nil {
		handler.record(output, err)
		domainError := publicDomainError(err, output.Request.TraceID)
		encoded, marshalErr := json.Marshal(contract.StreamEvent{Type: contract.StreamEventError, SchemaVersion: contract.SchemaVersion, TraceID: domainError.TraceID, RequestID: output.Request.RequestID, Error: domainError})
		if marshalErr == nil && context.Request.Context().Err() == nil {
			_, _ = fmt.Fprintf(context.Writer, "event: %s\ndata: %s\n\n", contract.StreamEventError, encoded)
			flusher.Flush()
		}
		return
	}
	setTraceHeaders(context, output)
	handler.record(output, nil)
}

func chatInput(context *gin.Context, request *AutoChatRequest) app.ChatInput {
	requestID, traceID := requestid.IDs(context)
	if request.ClientRequestID != "" && requestID == "" {
		requestID = request.ClientRequestID
	}
	userID := context.GetString("userName")
	return app.ChatInput{
		TraceID: traceID, RequestID: requestID, UserID: userID, TenantID: userID,
		SessionID: strings.TrimSpace(request.SessionID), Question: strings.TrimSpace(request.Message), Locale: "zh-CN", Debug: request.Debug,
	}
}

func (handler *AutoHandler) writeJSONError(context *gin.Context, err error) {
	_, traceID := requestid.IDs(context)
	domainError := publicDomainError(err, traceID)
	context.JSON(httpStatus(domainError.Category), domainError)
}

func publicDomainError(err error, traceID string) *contract.DomainError {
	var domainError *contract.DomainError
	if errors.As(err, &domainError) {
		return contract.WithTrace(domainError, firstNonEmpty(domainError.TraceID, traceID))
	}
	return contract.WithTrace(err, traceID)
}

func httpStatus(category contract.ErrorCategory) int {
	switch category {
	case contract.ErrorValidation:
		return http.StatusBadRequest
	case contract.ErrorAuth:
		return http.StatusUnauthorized
	case contract.ErrorNotFound:
		return http.StatusNotFound
	case contract.ErrorConflict:
		return http.StatusConflict
	case contract.ErrorDependencyTimeout:
		return http.StatusGatewayTimeout
	case contract.ErrorDependencyUnavailable, contract.ErrorModel:
		return http.StatusServiceUnavailable
	case contract.ErrorBudgetExceeded:
		return http.StatusTooManyRequests
	case contract.ErrorEvidenceInsufficient:
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}

func setTraceHeaders(context *gin.Context, output app.ChatOutput) {
	context.Header(requestid.RequestIDHeader, output.Request.RequestID)
	context.Header(requestid.TraceIDHeader, output.Request.TraceID)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (handler *AutoHandler) record(output app.ChatOutput, err error) {
	if handler.observer != nil {
		handler.observer.Record(output, err)
	}
}
