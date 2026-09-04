package router

import (
	"GopherAI/controller/agentrun"

	"github.com/gin-gonic/gin"
)

func RegisterAgentRunRouter(group *gin.RouterGroup) {
	handler := agentrun.NewDefaultHandler()
	group.POST("/diagnostics", handler.Start)
	group.GET("/:run_id", handler.Get)
	group.POST("/:run_id/resume", handler.Resume)
	group.POST("/:run_id/cancel", handler.Cancel)
}
