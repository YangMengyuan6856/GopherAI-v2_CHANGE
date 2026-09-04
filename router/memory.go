package router

import (
	"GopherAI/controller/memory"

	"github.com/gin-gonic/gin"
)

func RegisterMemoryRouter(group *gin.RouterGroup) {
	handler := memory.NewDefaultHandler()
	group.GET("/sessions/:session_id/context", handler.Preview)
	group.POST("/sessions/:session_id/rebuild", handler.Rebuild)
}
