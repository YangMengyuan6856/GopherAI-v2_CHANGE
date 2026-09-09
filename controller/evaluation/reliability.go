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

const defaultReliabilityReportPath = "/root/GopherAI_Runtime/evaluation/reliability-latest.json"

type ReliabilityHandler struct {
	run   ReliabilityRunner
	store reliabilityeval.ReportStore
}

func NewReliabilityHandler(run ReliabilityRunner) *ReliabilityHandler {
	return &ReliabilityHandler{run: run}
}

func NewReliabilityHandlerWithStore(run ReliabilityRunner, store reliabilityeval.ReportStore) *ReliabilityHandler {
	return &ReliabilityHandler{run: run, store: store}
}

func NewDefaultReliabilityHandler() *ReliabilityHandler {
	return NewReliabilityHandlerWithStore(reliabilityeval.Run, reliabilityeval.NewFileStore(defaultReliabilityReportPath))
}

func (handler *ReliabilityHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.store == nil {
		writeReliabilityError(ginContext, "RELIABILITY_REPORT_UNAVAILABLE", "可靠性验收报告暂不可用")
		return
	}
	report, err := handler.store.Load()
	if err != nil {
		writeReliabilityError(ginContext, "RELIABILITY_REPORT_NOT_READY", "请先运行可靠性故障验收")
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, report)
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
	if handler.store != nil {
		if err := handler.store.Save(report); err != nil {
			writeReliabilityError(ginContext, "RELIABILITY_REPORT_PERSIST_FAILED", "可靠性验收报告未能安全落盘")
			return
		}
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, report)
}

func writeReliabilityError(ginContext *gin.Context, code, message string) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(http.StatusServiceUnavailable, ErrorResponse{SchemaVersion: reliabilityeval.SchemaVersion, Code: code, Message: message, Retryable: true, TraceID: traceID})
}
