package router

import (
	"GopherAI/controller/memory"

	"github.com/gin-gonic/gin"
)

func RegisterMemoryRouter(group *gin.RouterGroup) {
	handler := memory.NewDefaultHandler()
	group.GET("/sessions/:session_id/context", handler.Preview)
	group.POST("/sessions/:session_id/rebuild", handler.Rebuild)
	group.GET("/profiles", handler.ListProfiles)
	group.PATCH("/profiles/:memory_id", handler.CorrectProfile)
	group.DELETE("/profiles/:memory_id", handler.DeleteProfile)
}
