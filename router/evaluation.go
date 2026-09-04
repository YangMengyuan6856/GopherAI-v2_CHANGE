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
	group.GET("/diagnostic/latest", handler.LatestDiagnostic)
	group.GET("/memory/latest", memoryHandler.LatestMemory)
	group.GET("/context/latest", contextHandler.LatestContext)
	group.GET("/tools/latest", toolHandler.LatestTool)
}
