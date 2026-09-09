package router

import (
	policycontroller "GopherAI/controller/policy"

	"github.com/gin-gonic/gin"
)

func RegisterPolicyRouter(group *gin.RouterGroup) {
	handler := policycontroller.NewDefaultHandler()
	group.GET("/active", handler.Active)
	group.POST("/simulate", handler.Simulate)
}
