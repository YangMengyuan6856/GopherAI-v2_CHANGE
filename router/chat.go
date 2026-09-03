package router

import (
	sessioncontroller "GopherAI/controller/session"

	"github.com/gin-gonic/gin"
)

func ChatRouter(router *gin.RouterGroup) {
	handler := sessioncontroller.NewDefaultAutoHandler()
	router.POST("/auto", handler.Chat)
	router.POST("/auto/stream", handler.Stream)
}
