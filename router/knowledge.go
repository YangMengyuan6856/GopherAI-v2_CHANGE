package router

import (
	"GopherAI/controller/knowledge"

	"github.com/gin-gonic/gin"
)

func RegisterKnowledgeRouter(group *gin.RouterGroup) {
	handler := knowledge.NewDefaultHandler()
	group.POST("/documents", handler.Upload)
	group.POST("/documents/:document_id/versions", handler.UploadVersion)
	group.POST("/documents/:document_id/rebuild", handler.Rebuild)
	group.DELETE("/documents/:document_id", handler.Delete)
	group.GET("/documents", handler.List)
	group.GET("/jobs/:job_id", handler.Job)
	group.POST("/search", handler.Search)
	group.POST("/answer", handler.Answer)
}
