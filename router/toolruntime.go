package router

import (
	toolruntimecontroller "GopherAI/controller/toolruntime"

	"github.com/gin-gonic/gin"
)

func RegisterToolRuntimeRouter(group *gin.RouterGroup) {
	handler := toolruntimecontroller.NewDefaultHandler()
	group.GET("", handler.Catalog)
	group.POST("/invoke", handler.Invoke)
	group.POST("/agent", handler.RunAgent)
}
