package evaluation

import (
	"GopherAI/common/mysql"
	"GopherAI/internal/failurepool"
	"GopherAI/middleware/requestid"
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type FailurePoolService interface {
	Audit(context.Context) (failurepool.Audit, error)
	RunCycle(context.Context) (failurepool.CycleResult, error)
}

type FailurePoolHandler struct {
	service FailurePoolService
	clock   func() time.Time
}

func NewFailurePoolHandler(service FailurePoolService, clock func() time.Time) *FailurePoolHandler {
	if clock == nil {
		clock = time.Now
	}
	return &FailurePoolHandler{service: service, clock: clock}
}

func NewDefaultFailurePoolHandler() *FailurePoolHandler {
	return NewFailurePoolHandler(failurepool.NewService(failurepool.NewGormRepository(mysql.DB), time.Now), time.Now)
}

func (handler *FailurePoolHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeFailurePoolError(ginContext, http.StatusServiceUnavailable, "FAILURE_POOL_UNAVAILABLE", "失败样本池暂不可用", true)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 4*time.Second)
	defer cancel()
	audit, err := handler.service.Audit(ctx)
	if err != nil {
		writeFailurePoolError(ginContext, http.StatusServiceUnavailable, "FAILURE_POOL_AUDIT_FAILED", "失败样本池读取失败", true)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, audit)
}

func (handler *FailurePoolHandler) Refresh(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeFailurePoolError(ginContext, http.StatusServiceUnavailable, "FAILURE_POOL_UNAVAILABLE", "失败样本池暂不可用", true)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 15*time.Second)
	defer cancel()
	result, err := handler.service.RunCycle(ctx)
	if err != nil {
		writeFailurePoolError(ginContext, http.StatusServiceUnavailable, "FAILURE_POOL_REFRESH_FAILED", "失败样本聚类失败", true)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, result)
}

func (handler *FailurePoolHandler) Acceptance(ginContext *gin.Context) {
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, failurepool.RunAcceptance(handler.clock().UTC()))
}

func writeFailurePoolError(ginContext *gin.Context, status int, code, message string, retryable bool) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(status, ErrorResponse{SchemaVersion: failurepool.SchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}
