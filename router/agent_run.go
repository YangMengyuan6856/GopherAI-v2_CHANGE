package router

import (
	"GopherAI/controller/agentrun"

	"github.com/gin-gonic/gin"
)

func RegisterAgentRunRouter(group *gin.RouterGroup) {
	handler := agentrun.NewDefaultHandler()
	caseShadowHandler := agentrun.NewDefaultCaseShadowHandler()
	collaborationPlanHandler := agentrun.NewDefaultCollaborationPlanHandler()
	collaborationShadowHandler := agentrun.NewDefaultCollaborationShadowHandler()
	group.POST("/diagnostics", handler.Start)
	group.POST("/diagnostics/case-shadow", caseShadowHandler.Analyze)
	group.POST("/diagnostics/collaboration-plan", collaborationPlanHandler.Plan)
	group.POST("/diagnostics/collaboration-shadow", collaborationShadowHandler.Run)
	group.GET("/:run_id", handler.Get)
	group.GET("/:run_id/context-compression", handler.ContextCompression)
	group.POST("/:run_id/resume", handler.Resume)
	group.POST("/:run_id/cancel", handler.Cancel)
	group.POST("/:run_id/resolution-proposals", handler.PreviewResolution)
	group.POST("/:run_id/resolution-confirmations", handler.ConfirmResolution)
	group.GET("/:run_id/resolution", handler.GetResolution)
}
