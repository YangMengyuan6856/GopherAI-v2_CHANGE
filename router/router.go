package router

import (
	"GopherAI/middleware/jwt"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

func InitRouter() *gin.Engine {

	r := gin.Default()
	registerHealthRoutes(r)
	enterRouter := r.Group("/api/v1")
	{
		RegisterUserRouter(enterRouter.Group("/user"))
	}
	//后续登录的接口需要jwt鉴权
	{
		AIGroup := enterRouter.Group("/AI")
		AIGroup.Use(jwt.Auth())
		AIRouter(AIGroup)
	}
	{
		ChatGroup := enterRouter.Group("/chat")
		ChatGroup.Use(requestid.Attach(), jwt.Auth())
		ChatRouter(ChatGroup)
	}

	{
		FileGroup := enterRouter.Group("/file")
		FileGroup.Use(jwt.Auth())
		FileRouter(FileGroup)
	}

	return r
}
