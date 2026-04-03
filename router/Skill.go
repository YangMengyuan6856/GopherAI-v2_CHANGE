package router

import (
	skillctl "GopherAI/controller/skill"

	"github.com/gin-gonic/gin"
)

// SkillRouter 注册技能相关路由（均需 JWT 鉴权，由外层中间件保证）
func SkillRouter(r *gin.RouterGroup) {
	// GET  /api/v1/skill/list         → 所有可用技能列表（公开，登录即可查看）
	r.GET("/list", skillctl.ListSkills)
	// GET  /api/v1/skill/user         → 当前用户已启用的技能
	r.GET("/user", skillctl.GetUserSkills)
	// POST /api/v1/skill/enable       → 为当前用户启用技能
	r.POST("/enable", skillctl.EnableSkill)
	// POST /api/v1/skill/disable      → 为当前用户禁用技能
	r.POST("/disable", skillctl.DisableSkill)
	// GET  /api/v1/skill/invocations  → 当前用户调用日志
	r.GET("/invocations", skillctl.GetInvocations)
}
