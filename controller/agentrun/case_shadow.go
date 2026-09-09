package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"GopherAI/common/mysql"
	"GopherAI/internal/diagnostic"
	"GopherAI/internal/incident"
	"GopherAI/internal/observability"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const maximumCaseShadowBodyBytes = 32 * 1024

type CaseStrategy interface {
	Analyze(context.Context, string, string, string) (diagnostic.CaseStrategyResult, error)
}

type CaseStrategyObserver interface {
	RecordCaseStrategy(string, string, time.Duration)
}

type CaseShadowHandler struct {
	strategy CaseStrategy
	observer CaseStrategyObserver
}

type CaseShadowRequest struct {
	Message string `json:"message"`
}

func NewCaseShadowHandler(strategy CaseStrategy, observer CaseStrategyObserver) *CaseShadowHandler {
	return &CaseShadowHandler{strategy: strategy, observer: observer}
}

func NewDefaultCaseShadowHandler() *CaseShadowHandler {
	strategy, err := diagnostic.NewCaseBasedStrategy(diagnostic.NewAgent(), incident.NewGormRepository(mysql.DB), 1200*time.Millisecond)
	if err != nil {
		panic(err)
	}
	return NewCaseShadowHandler(strategy, observability.DefaultMetrics())
}

func (handler *CaseShadowHandler) Analyze(ctx *gin.Context) {
	startedAt := time.Now()
	if handler == nil || handler.strategy == nil {
		handler.writeCaseError(ctx, http.StatusServiceUnavailable, "CASE_STRATEGY_UNAVAILABLE", "案例增强策略暂时不可用", true)
		return
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maximumCaseShadowBodyBytes)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	request := new(CaseShadowRequest)
	if err := decoder.Decode(request); err != nil || caseRequestEOF(decoder) != nil || strings.TrimSpace(request.Message) == "" {
		handler.observeCase("none", "error", startedAt)
		handler.writeCaseError(ctx, http.StatusBadRequest, "CASE_STRATEGY_REQUEST_INVALID", "请输入字段受限的故障现象 JSON", false)
		return
	}
	userID := strings.TrimSpace(ctx.GetString("userName"))
	if userID == "" {
		handler.observeCase("none", "error", startedAt)
		handler.writeCaseError(ctx, http.StatusUnauthorized, "CASE_STRATEGY_PRINCIPAL_MISSING", "无法确认当前登录用户", false)
		return
	}
	result, err := handler.strategy.Analyze(ctx.Request.Context(), userID, userID, request.Message)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			handler.observeCase("none", "cancelled", startedAt)
			handler.writeCaseError(ctx, 499, "CASE_STRATEGY_CANCELLED", "案例增强演算已取消", true)
			return
		}
		handler.observeCase("none", "error", startedAt)
		handler.writeCaseError(ctx, http.StatusServiceUnavailable, "CASE_STRATEGY_FAILED", "案例增强演算暂时失败", true)
		return
	}
	outcome := "success"
	if result.FallbackStrategy != "" {
		outcome = "fallback"
	}
	handler.observeCase(result.CaseStrength, outcome, startedAt)
	ctx.JSON(http.StatusOK, result)
}

func (handler *CaseShadowHandler) observeCase(strength string, outcome string, startedAt time.Time) {
	if handler != nil && handler.observer != nil {
		handler.observer.RecordCaseStrategy(strength, outcome, time.Since(startedAt))
	}
}

func (handler *CaseShadowHandler) writeCaseError(ctx *gin.Context, status int, code string, message string, retryable bool) {
	_, traceID := requestid.IDs(ctx)
	ctx.JSON(status, ErrorResponse{SchemaVersion: diagnostic.CaseStrategySchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}

func caseRequestEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}
