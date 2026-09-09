package evaluation

import (
	"context"
	"net/http"

	"GopherAI/internal/observability"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

type GrafanaRuntimeReader interface {
	Snapshot(context.Context) (observability.GrafanaRuntimeSnapshot, error)
}

type GrafanaRuntimeHandler struct {
	reader GrafanaRuntimeReader
}

func NewGrafanaRuntimeHandler(reader GrafanaRuntimeReader) *GrafanaRuntimeHandler {
	return &GrafanaRuntimeHandler{reader: reader}
}

func NewDefaultGrafanaRuntimeHandler() *GrafanaRuntimeHandler {
	return NewGrafanaRuntimeHandler(observability.NewDefaultGrafanaRuntimeClient())
}

func (handler *GrafanaRuntimeHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.reader == nil {
		writeGrafanaUnavailable(ginContext)
		return
	}
	requestContext, cancel := context2SecondTimeout(ginContext)
	defer cancel()
	snapshot, err := handler.reader.Snapshot(requestContext)
	if err != nil {
		writeGrafanaUnavailable(ginContext)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, gin.H{"schema_version": "grafana-runtime-response-v1", "snapshot": snapshot})
}

func writeGrafanaUnavailable(ginContext *gin.Context) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(http.StatusServiceUnavailable, ErrorResponse{
		SchemaVersion: "grafana-runtime-response-v1", Code: "GRAFANA_RUNTIME_UNAVAILABLE",
		Message: "生产可观测看板暂不可用", Retryable: true, TraceID: traceID,
	})
}
