package router

import (
	sessioncontroller "GopherAI/controller/session"

	"github.com/gin-gonic/gin"
)

func ChatRouter(router *gin.RouterGroup) {
	handler := sessioncontroller.NewDefaultAutoHandler()
	feedbackHandler := sessioncontroller.NewDefaultFeedbackHandler()
	router.POST("/auto", handler.Chat)
	router.POST("/auto/stream", handler.Stream)
	router.POST("/messages/:request_id/feedback", feedbackHandler.Submit)
}
