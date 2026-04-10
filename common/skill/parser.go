package skill

import "strings"

const skillPrefix = "/skill"

// ParseCommand 解析 /skill <code> [arg1 arg2 ...] 格式的命令。
//
// 支持两种参数风格：
//  1. key=value 显式参数：/skill weather city=北京
//  2. 位置参数（向后兼容）：/skill weather 北京 → args={"city":"北京","query":"北京"}
//
// 示例：
//
//	/skill weather 北京           → code="weather", args={"city":"北京","query":"北京"}, ok=true
//	/skill translate Hello World  → code="translate", args={"query":"Hello World"},     ok=true
//	/skill calculator 1+2*3      → code="calculator", args={"query":"1+2*3"},           ok=true
//	/skill datetime              → code="datetime", args={},                            ok=true
//	普通消息                      →                                                      ok=false
func ParseCommand(input string) (code string, args map[string]string, ok bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, skillPrefix) {
		return "", nil, false
	}

	rest := strings.TrimSpace(trimmed[len(skillPrefix):])
	if rest == "" {
		return "", nil, false
	}

	parts := strings.Fields(rest)
	code = parts[0]
	args = make(map[string]string)

	if len(parts) > 1 {
		argParts := parts[1:]

		// 检测是否有 key=value 风格的参数
		hasKV := false
		for _, p := range argParts {
			if strings.Contains(p, "=") {
				hasKV = true
				break
			}
		}

		if hasKV {
			// key=value 模式：逐个解析
			var plainParts []string
			for _, p := range argParts {
				if idx := strings.Index(p, "="); idx > 0 {
					key := p[:idx]
					val := p[idx+1:]
					args[key] = val
				} else {
					plainParts = append(plainParts, p)
				}
			}
			// 未匹配 key=value 的部分拼入 query
			if len(plainParts) > 0 {
				args["query"] = strings.Join(plainParts, " ")
			}
		} else {
			// 纯位置参数模式（向后兼容）
			argText := strings.Join(argParts, " ")
			args["query"] = argText
			args["city"] = argText // 天气技能约定参数
		}
	}

	return code, args, true
}

// IsSkillCommand 快速判断输入是否为技能命令
func IsSkillCommand(input string) bool {
	return strings.HasPrefix(strings.TrimSpace(input), skillPrefix)
}
