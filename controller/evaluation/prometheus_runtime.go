package evaluation

import (
	"context"
	"net/http"
	"time"

	"GopherAI/internal/observability"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

type PrometheusRuntimeReader interface {
	Snapshot(context.Context) (observability.PrometheusRuntimeSnapshot, error)
}

type PrometheusRuntimeHandler struct {
	reader PrometheusRuntimeReader
}

func NewPrometheusRuntimeHandler(reader PrometheusRuntimeReader) *PrometheusRuntimeHandler {
	return &PrometheusRuntimeHandler{reader: reader}
}

func NewDefaultPrometheusRuntimeHandler() *PrometheusRuntimeHandler {
	return NewPrometheusRuntimeHandler(observability.NewDefaultPrometheusRuntimeClient())
}

func (handler *PrometheusRuntimeHandler) Latest(context *gin.Context) {
	if handler == nil || handler.reader == nil {
		writePrometheusUnavailable(context)
		return
	}
	requestContext, cancel := context2SecondTimeout(context)
	defer cancel()
	snapshot, err := handler.reader.Snapshot(requestContext)
	if err != nil {
		writePrometheusUnavailable(context)
		return
	}
	context.Header("Cache-Control", "no-store")
	context.JSON(http.StatusOK, gin.H{"schema_version": "prometheus-runtime-response-v1", "snapshot": snapshot})
}

func context2SecondTimeout(ginContext *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ginContext.Request.Context(), 2*time.Second)
}

func writePrometheusUnavailable(context *gin.Context) {
	_, traceID := requestid.IDs(context)
	context.JSON(http.StatusServiceUnavailable, ErrorResponse{
		SchemaVersion: "prometheus-runtime-response-v1", Code: "PROMETHEUS_RUNTIME_UNAVAILABLE",
		Message: "生产指标聚合暂不可用", Retryable: true, TraceID: traceID,
	})
}
