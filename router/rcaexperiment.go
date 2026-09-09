package router

import (
	rcacontroller "GopherAI/controller/rcaexperiment"
	"github.com/gin-gonic/gin"
)

func RegisterRCAExperimentRouter(group *gin.RouterGroup) {
	h := rcacontroller.NewDefaultHandler()
	group.GET("", h.Catalog)
	group.GET("/observations/:id", h.Observation)
	group.POST("/diagnose", h.Diagnose)
	group.GET("/report", h.Report)
	group.GET("/agent-report", h.AgentReport)
	group.GET("/agent-report/:id", h.AgentReport)
}
