package evaluation

import (
	"GopherAI/internal/reliabilityeval"
	"GopherAI/middleware/requestid"
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type ReliabilityRunner func(context.Context) (reliabilityeval.Report, error)

type ReliabilityHandler struct{ run ReliabilityRunner }

func NewReliabilityHandler(run ReliabilityRunner) *ReliabilityHandler {
	return &ReliabilityHandler{run: run}
}

func NewDefaultReliabilityHandler() *ReliabilityHandler {
	return NewReliabilityHandler(reliabilityeval.Run)
}

func (handler *ReliabilityHandler) Acceptance(ginContext *gin.Context) {
	if handler == nil || handler.run == nil {
		writeReliabilityError(ginContext, "RELIABILITY_ACCEPTANCE_UNAVAILABLE", "可靠性验收暂不可用")
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 6*time.Second)
	defer cancel()
	report, err := handler.run(ctx)
	if err != nil {
		writeReliabilityError(ginContext, "RELIABILITY_ACCEPTANCE_FAILED", "可靠性故障验收未完成")
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, report)
}

func writeReliabilityError(ginContext *gin.Context, code, message string) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(http.StatusServiceUnavailable, ErrorResponse{SchemaVersion: reliabilityeval.SchemaVersion, Code: code, Message: message, Retryable: true, TraceID: traceID})
}
