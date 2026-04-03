package skill

import (
	skillpkg "GopherAI/common/skill"
	daoskill "GopherAI/dao/skill"
	"GopherAI/model"
	"fmt"
)

const defaultInvocationLimit = 50

// SkillInfo 面向接口的技能描述，用于返回给前端
type SkillInfo struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ListRegisteredSkills 列出当前注册中心内所有技能（运行时有效，不查 DB）
func ListRegisteredSkills() []SkillInfo {
	skills := skillpkg.GetRegistry().All()
	result := make([]SkillInfo, 0, len(skills))
	for _, s := range skills {
		result = append(result, SkillInfo{
			Code:        s.Code(),
			Name:        s.Name(),
			Description: s.Description(),
		})
	}
	return result
}

// GetUserEnabledSkillCodes 获取用户已启用的技能 code 列表
func GetUserEnabledSkillCodes(userName string) ([]string, error) {
	userSkills, err := daoskill.GetUserEnabledSkills(userName)
	if err != nil {
		return nil, err
	}
	codes := make([]string, 0, len(userSkills))
	for _, us := range userSkills {
		codes = append(codes, us.SkillCode)
	}
	return codes, nil
}

// EnableUserSkill 为用户启用某个技能（先校验技能是否在注册中心存在）
func EnableUserSkill(userName, skillCode string) error {
	if _, ok := skillpkg.GetRegistry().Get(skillCode); !ok {
		return fmt.Errorf("技能 [%s] 不存在或未注册", skillCode)
	}
	return daoskill.EnableUserSkill(userName, skillCode)
}

// DisableUserSkill 为用户禁用某个技能
func DisableUserSkill(userName, skillCode string) error {
	return daoskill.DisableUserSkill(userName, skillCode)
}

// GetUserInvocations 获取用户最近 N 条技能调用日志
func GetUserInvocations(userName string) ([]model.SkillInvocation, error) {
	return daoskill.GetUserInvocations(userName, defaultInvocationLimit)
}
