package router

import (
	"net/http"
	"strings"

	"GopherAI/internal/observability"
	"GopherAI/middleware/jwt"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

func InitRouter() *gin.Engine {

	r := gin.Default()
	r.NoRoute(retiredEntryPoint)
	registerHealthRoutes(r)
	r.GET("/metrics", gin.WrapH(observability.MetricsHandler()))
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
		KnowledgeGroup := enterRouter.Group("/knowledge")
		KnowledgeGroup.Use(requestid.Attach(), jwt.Auth())
		RegisterKnowledgeRouter(KnowledgeGroup)
	}
	{
		AgentRunGroup := enterRouter.Group("/agent-runs")
		AgentRunGroup.Use(requestid.Attach(), jwt.Auth())
		RegisterAgentRunRouter(AgentRunGroup)
	}
	{
		EvaluationGroup := enterRouter.Group("/evaluations")
		EvaluationGroup.Use(requestid.Attach(), jwt.Auth())
		RegisterEvaluationRouter(EvaluationGroup)
	}
	{
		MemoryGroup := enterRouter.Group("/memory")
		MemoryGroup.Use(requestid.Attach(), jwt.Auth())
		RegisterMemoryRouter(MemoryGroup)
	}
	{
		ToolGroup := enterRouter.Group("/tools")
		ToolGroup.Use(requestid.Attach(), jwt.Auth())
		RegisterToolRuntimeRouter(ToolGroup)
	}
	{
		PolicyGroup := enterRouter.Group("/policies")
		PolicyGroup.Use(requestid.Attach(), jwt.Auth())
		RegisterPolicyRouter(PolicyGroup)
	}

	{
		FileGroup := enterRouter.Group("/file")
		FileGroup.Use(jwt.Auth())
		FileRouter(FileGroup)
	}

	return r
}

func retiredEntryPoint(ctx *gin.Context) {
	path := strings.ToLower(ctx.Request.URL.Path)
	if path == "/api/v1/skill" || strings.HasPrefix(path, "/api/v1/skill/") {
		observability.DefaultMetrics().RecordLegacyEntryAttempt("skill_api")
		ctx.JSON(http.StatusGone, gin.H{
			"code":        "LEGACY_SKILL_RETIRED",
			"message":     "旧 Skill 入口已退役，请使用受治理 Tool Runtime",
			"replacement": "/api/v1/tools/catalog",
		})
		return
	}
	ctx.String(http.StatusNotFound, "404 page not found")
}
