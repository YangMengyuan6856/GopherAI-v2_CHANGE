package router

import (
	evaluationcontroller "GopherAI/controller/evaluation"

	"github.com/gin-gonic/gin"
)

func RegisterEvaluationRouter(group *gin.RouterGroup) {
	handler := evaluationcontroller.NewDefaultHandler()
	memoryHandler := evaluationcontroller.NewDefaultMemoryHandler()
	contextHandler := evaluationcontroller.NewDefaultContextHandler()
	toolHandler := evaluationcontroller.NewDefaultToolHandler()
	collaborationHandler := evaluationcontroller.NewDefaultCollaborationHandler()
	parentContextHandler := evaluationcontroller.NewDefaultParentContextHandler()
	catalogHandler := evaluationcontroller.NewDefaultCatalogHandler()
	unifiedHandler := evaluationcontroller.NewDefaultUnifiedHandler()
	anomalyHandler := evaluationcontroller.NewAnomalyHandler()
	metricCatalogHandler := evaluationcontroller.NewDefaultMetricCatalogHandler()
	prometheusRuntimeHandler := evaluationcontroller.NewDefaultPrometheusRuntimeHandler()
	controlWebhookHandler := evaluationcontroller.NewDefaultControlWebhookHandler()
	group.GET("/diagnostic/latest", handler.LatestDiagnostic)
	group.GET("/memory/latest", memoryHandler.LatestMemory)
	group.GET("/context/latest", contextHandler.LatestContext)
	group.GET("/tools/latest", toolHandler.LatestTool)
	group.GET("/collaboration/latest", collaborationHandler.Latest)
	group.GET("/parent-context/latest", parentContextHandler.Latest)
	group.GET("/catalog/latest", catalogHandler.Latest)
	group.GET("/unified/latest", unifiedHandler.Latest)
	group.POST("/anomaly/simulate", anomalyHandler.Simulate)
	group.GET("/anomaly/production/latest", anomalyHandler.ProductionLatest)
	group.GET("/metrics/catalog", metricCatalogHandler.Latest)
	group.GET("/metrics/runtime", prometheusRuntimeHandler.Latest)
	group.GET("/webhooks/latest", controlWebhookHandler.Latest)
	group.POST("/webhooks/acceptance", controlWebhookHandler.Acceptance)
}
