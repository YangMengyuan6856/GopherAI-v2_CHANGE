package skill

import (
	"GopherAI/common/mysql"
	"GopherAI/model"
	"log"
	"time"
)

// GetAllActiveSkills 查询所有状态为启用的技能定义
func GetAllActiveSkills() ([]model.Skill, error) {
	var skills []model.Skill
	err := mysql.DB.Where("status = ?", 1).Find(&skills).Error
	return skills, err
}

// GetUserEnabledSkills 查询某用户已启用的技能列表
func GetUserEnabledSkills(userName string) ([]model.UserSkill, error) {
	var us []model.UserSkill
	err := mysql.DB.Where("username = ? AND enabled = ?", userName, true).Find(&us).Error
	return us, err
}

// EnableUserSkill 为用户启用一个技能，若记录已存在则更新，否则新建
func EnableUserSkill(userName, skillCode string) error {
	var us model.UserSkill
	result := mysql.DB.Where("username = ? AND skill_code = ?", userName, skillCode).First(&us)
	if result.Error != nil {
		us = model.UserSkill{UserName: userName, SkillCode: skillCode, Enabled: true}
		return mysql.DB.Create(&us).Error
	}
	return mysql.DB.Model(&us).Update("enabled", true).Error
}

// DisableUserSkill 为用户禁用一个技能
func DisableUserSkill(userName, skillCode string) error {
	return mysql.DB.Model(&model.UserSkill{}).
		Where("username = ? AND skill_code = ?", userName, skillCode).
		Update("enabled", false).Error
}

// GetUserInvocations 获取用户的技能调用历史，按时间倒序
func GetUserInvocations(userName string, limit int) ([]model.SkillInvocation, error) {
	var invocations []model.SkillInvocation
	err := mysql.DB.
		Where("username = ?", userName).
		Order("created_at DESC").
		Limit(limit).
		Find(&invocations).Error
	return invocations, err
}

// SaveInvocation 写入技能调用日志
func SaveInvocation(inv *model.SkillInvocation) error {
	return mysql.DB.Create(inv).Error
}

// UpsertSkill 注册或更新技能元数据到 DB（按 code 去重）
func UpsertSkill(s *model.Skill) error {
	var existing model.Skill
	result := mysql.DB.Where("code = ?", s.Code).First(&existing)
	if result.Error != nil {
		return mysql.DB.Create(s).Error
	}
	return mysql.DB.Model(&existing).Updates(map[string]interface{}{
		"name":        s.Name,
		"description": s.Description,
		"type":        s.Type,
		"status":      s.Status,
		"config_json": s.ConfigJSON,
		"tags":        s.Tags,
	}).Error
}

// IsUserSkillEnabled 检查某用户是否启用了指定技能
func IsUserSkillEnabled(userName, skillCode string) (bool, error) {
	var us model.UserSkill
	result := mysql.DB.Where("username = ? AND skill_code = ? AND enabled = ?", userName, skillCode, true).First(&us)
	if result.Error != nil {
		if result.RowsAffected == 0 {
			return false, nil
		}
		return false, result.Error
	}
	return true, nil
}

// DBLogger 实现 skill.InvocationLogger 接口，将调用日志异步写入数据库
type DBLogger struct{}

// Log 异步写入调用日志，不阻塞技能执行链路
func (l *DBLogger) Log(traceID, userName, sessionID, skillCode, inputJSON, outputJSON, status string, latencyMs int64, errMsg string) {
	go func() {
		inv := &model.SkillInvocation{
			TraceID:    traceID,
			UserName:   userName,
			SessionID:  sessionID,
			SkillCode:  skillCode,
			InputJSON:  inputJSON,
			OutputJSON: outputJSON,
			Status:     status,
			LatencyMs:  latencyMs,
			Error:      errMsg,
			CreatedAt:  time.Now(),
		}
		if err := SaveInvocation(inv); err != nil {
			log.Printf("[SkillDAO] failed to save invocation log trace=%s: %v", traceID, err)
		}
	}()
}
