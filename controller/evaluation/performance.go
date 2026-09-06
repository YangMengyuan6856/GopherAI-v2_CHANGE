package evaluation

import (
	"GopherAI/internal/perfeval"
	"GopherAI/middleware/requestid"
	"net/http"

	"github.com/gin-gonic/gin"
)

type PerformanceReportStore interface {
	Load() (perfeval.Report, error)
}

type filePerformanceReportStore struct{ path string }

func (store filePerformanceReportStore) Load() (perfeval.Report, error) {
	return perfeval.LoadReport(store.path)
}

type PerformanceHandler struct{ store PerformanceReportStore }

func NewPerformanceHandler(store PerformanceReportStore) *PerformanceHandler {
	return &PerformanceHandler{store: store}
}

func NewDefaultPerformanceHandler() *PerformanceHandler {
	return NewPerformanceHandler(filePerformanceReportStore{path: "/root/GopherAI_Runtime/perf/latest.json"})
}

func (handler *PerformanceHandler) Latest(context *gin.Context) {
	if handler == nil || handler.store == nil {
		writePerformanceError(context, "PERFORMANCE_REPORT_UNAVAILABLE", "性能验收报告暂不可用")
		return
	}
	report, err := handler.store.Load()
	if err != nil {
		writePerformanceError(context, "PERFORMANCE_REPORT_NOT_READY", "尚未生成有效的性能验收报告")
		return
	}
	context.Header("Cache-Control", "no-store")
	context.JSON(http.StatusOK, report)
}

func writePerformanceError(context *gin.Context, code, message string) {
	_, traceID := requestid.IDs(context)
	context.JSON(http.StatusServiceUnavailable, ErrorResponse{SchemaVersion: perfeval.SchemaVersion, Code: code, Message: message, Retryable: true, TraceID: traceID})
}
