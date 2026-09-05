package evaluation

import (
	"net/http"

	"GopherAI/internal/knowledge"
	"GopherAI/internal/metriccatalog"
	"GopherAI/internal/observability"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

const metricCatalogResponseSchemaVersion = "metric-catalog-summary-v1"

type MetricCatalogResponse struct {
	SchemaVersion string                      `json:"schema_version"`
	Report        metriccatalog.CatalogReport `json:"report"`
}

type MetricCatalogHandler struct {
	report metriccatalog.CatalogReport
	err    error
}

func NewDefaultMetricCatalogHandler() *MetricCatalogHandler {
	backendRegistry := prometheus.NewRegistry()
	backendMetrics := observability.NewMetrics(backendRegistry, backendRegistry)
	workerRegistry := prometheus.NewRegistry()
	workerMetrics := knowledge.NewWorkerMetrics(workerRegistry)
	report, err := metriccatalog.Audit(
		metriccatalog.CollectorSet{Component: "backend", Collectors: backendMetrics.Collectors()},
		metriccatalog.CollectorSet{Component: "index_worker", Collectors: workerMetrics.Collectors()},
	)
	return &MetricCatalogHandler{report: report, err: err}
}

func (handler *MetricCatalogHandler) Latest(context *gin.Context) {
	if handler == nil || handler.err != nil || !handler.report.Passed {
		_, traceID := requestid.IDs(context)
		context.JSON(http.StatusServiceUnavailable, ErrorResponse{
			SchemaVersion: metricCatalogResponseSchemaVersion, Code: "METRIC_CATALOG_AUDIT_FAILED",
			Message: "指标目录或标签基数审计未通过", Retryable: false, TraceID: traceID,
		})
		return
	}
	etag := `"` + handler.report.CatalogSHA256 + `"`
	context.Header("ETag", etag)
	context.Header("Cache-Control", "private, max-age=60")
	if context.GetHeader("If-None-Match") == etag {
		context.Status(http.StatusNotModified)
		return
	}
	context.JSON(http.StatusOK, MetricCatalogResponse{SchemaVersion: metricCatalogResponseSchemaVersion, Report: handler.report})
}
