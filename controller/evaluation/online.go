package evaluation

import (
	"GopherAI/common/mysql"
	"GopherAI/internal/onlineeval"
	"GopherAI/middleware/requestid"
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type OnlineEvaluationService interface {
	Audit(context.Context) (onlineeval.Audit, error)
	RunAcceptance(context.Context, string) (onlineeval.AcceptanceReport, error)
}

type OnlineEvaluationHandler struct{ service OnlineEvaluationService }

func NewOnlineEvaluationHandler(service OnlineEvaluationService) *OnlineEvaluationHandler {
	return &OnlineEvaluationHandler{service: service}
}

func NewDefaultOnlineEvaluationHandler() *OnlineEvaluationHandler {
	return NewOnlineEvaluationHandler(onlineeval.NewService(onlineeval.NewGormRepository(mysql.DB), time.Now))
}

func (handler *OnlineEvaluationHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeOnlineEvaluationError(ginContext, http.StatusServiceUnavailable, "ONLINE_EVAL_UNAVAILABLE", "在线评测服务暂不可用", true)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 3*time.Second)
	defer cancel()
	audit, err := handler.service.Audit(ctx)
	if err != nil {
		writeOnlineEvaluationError(ginContext, http.StatusServiceUnavailable, "ONLINE_EVAL_AUDIT_UNAVAILABLE", "在线评测审计暂不可用", true)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, audit)
}

func (handler *OnlineEvaluationHandler) Acceptance(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeOnlineEvaluationError(ginContext, http.StatusServiceUnavailable, "ONLINE_EVAL_UNAVAILABLE", "在线评测服务暂不可用", true)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 10*time.Second)
	defer cancel()
	report, err := handler.service.RunAcceptance(ctx, ginContext.GetString("userName"))
	if err != nil {
		writeOnlineEvaluationError(ginContext, http.StatusServiceUnavailable, "ONLINE_EVAL_ACCEPTANCE_FAILED", "在线评测异步链路验收失败", true)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, report)
}

func writeOnlineEvaluationError(ginContext *gin.Context, status int, code, message string, retryable bool) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(status, ErrorResponse{SchemaVersion: onlineeval.AuditSchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}
