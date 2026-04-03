package skill

import (
	"GopherAI/common/code"
	"GopherAI/controller"
	"GopherAI/model"
	skillsvc "GopherAI/service/skill"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ---- 响应体定义 ----

type ListSkillsResponse struct {
	controller.Response
	Skills []skillsvc.SkillInfo `json:"skills,omitempty"`
}

type UserSkillRequest struct {
	SkillCode string `json:"skill_code" binding:"required"`
}

type UserSkillsResponse struct {
	controller.Response
	SkillCodes []string `json:"skill_codes,omitempty"`
}

type InvocationsResponse struct {
	controller.Response
	Invocations []model.SkillInvocation `json:"invocations,omitempty"`
}

// ---- Handler 实现 ----

// ListSkills GET /api/v1/skill/list
// 返回当前注册中心内所有可用技能列表
func ListSkills(c *gin.Context) {
	res := new(ListSkillsResponse)
	res.Success()
	res.Skills = skillsvc.ListRegisteredSkills()
	c.JSON(http.StatusOK, res)
}

// GetUserSkills GET /api/v1/skill/user
// 返回当前登录用户已启用的技能 code 列表
func GetUserSkills(c *gin.Context) {
	res := new(UserSkillsResponse)
	userName := c.GetString("userName")

	codes, err := skillsvc.GetUserEnabledSkillCodes(userName)
	if err != nil {
		c.JSON(http.StatusOK, res.CodeOf(code.CodeServerBusy))
		return
	}
	res.Success()
	res.SkillCodes = codes
	c.JSON(http.StatusOK, res)
}

// EnableSkill POST /api/v1/skill/enable
// 为当前登录用户启用指定技能
func EnableSkill(c *gin.Context) {
	res := new(controller.Response)
	userName := c.GetString("userName")

	var req UserSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, res.CodeOf(code.CodeInvalidParams))
		return
	}
	if err := skillsvc.EnableUserSkill(userName, req.SkillCode); err != nil {
		c.JSON(http.StatusOK, res.CodeOf(code.CodeServerBusy))
		return
	}
	res.Success()
	c.JSON(http.StatusOK, res)
}

// DisableSkill POST /api/v1/skill/disable
// 为当前登录用户禁用指定技能
func DisableSkill(c *gin.Context) {
	res := new(controller.Response)
	userName := c.GetString("userName")

	var req UserSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, res.CodeOf(code.CodeInvalidParams))
		return
	}
	if err := skillsvc.DisableUserSkill(userName, req.SkillCode); err != nil {
		c.JSON(http.StatusOK, res.CodeOf(code.CodeServerBusy))
		return
	}
	res.Success()
	c.JSON(http.StatusOK, res)
}

// GetInvocations GET /api/v1/skill/invocations
// 返回当前用户最近 50 条技能调用日志
func GetInvocations(c *gin.Context) {
	res := new(InvocationsResponse)
	userName := c.GetString("userName")

	invocations, err := skillsvc.GetUserInvocations(userName)
	if err != nil {
		c.JSON(http.StatusOK, res.CodeOf(code.CodeServerBusy))
		return
	}
	res.Success()
	res.Invocations = invocations
	c.JSON(http.StatusOK, res)
}
