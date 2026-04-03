package skill

import "strings"

const skillPrefix = "/skill"

// ParseCommand 解析 /skill <code> [arg1 arg2 ...] 格式的命令。
//
// 示例：
//   /skill weather 北京      → code="weather", args={"city":"北京","query":"北京"}, ok=true
//   /skill weather           → code="weather", args={},                           ok=true
//   普通消息                  →                                                    ok=false
func ParseCommand(input string) (code string, args map[string]string, ok bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, skillPrefix) {
		return "", nil, false
	}

	// 去掉 "/skill" 前缀后的剩余部分
	rest := strings.TrimSpace(trimmed[len(skillPrefix):])
	if rest == "" {
		return "", nil, false
	}

	parts := strings.Fields(rest)
	code = parts[0]
	args = make(map[string]string)

	if len(parts) > 1 {
		// 剩余部分作为 "query"（通用）和 "city"（天气技能约定参数）
		argText := strings.Join(parts[1:], " ")
		args["query"] = argText
		args["city"] = argText // 天气技能直接使用 city 键
	}

	return code, args, true
}

// IsSkillCommand 快速判断输入是否为技能命令
func IsSkillCommand(input string) bool {
	return strings.HasPrefix(strings.TrimSpace(input), skillPrefix)
}
