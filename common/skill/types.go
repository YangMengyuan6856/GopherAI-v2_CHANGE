package skill

import "context"

// ExecuteRequest 技能执行请求，统一封装调用上下文
type ExecuteRequest struct {
	UserName  string
	SessionID string
	RawInput  string            // 用户原始输入（含 /skill 前缀）
	Args      map[string]string // 解析出的参数键值对
}

// ExecuteResult 技能执行结果
type ExecuteResult struct {
	SkillCode string
	Output    string                 // 返回给用户的文本结果
	Data      map[string]interface{} // 可选的结构化数据
}

// Skill 技能统一接口，所有内置/外部技能都必须实现此接口
type Skill interface {
	// Code 技能唯一标识，例如 "weather"
	Code() string
	// Name 技能可读名称，例如 "天气查询"
	Name() string
	// Description 技能描述，用于向用户展示
	Description() string
	// Execute 执行技能，返回结果或错误
	Execute(ctx context.Context, req *ExecuteRequest) (*ExecuteResult, error)
}

// InvocationLogger 技能调用日志写入接口
// 通过接口注入，避免 common/skill 直接依赖 dao 层（防止循环依赖）
type InvocationLogger interface {
	Log(traceID, userName, sessionID, skillCode, inputJSON, outputJSON, status string, latencyMs int64, errMsg string)
}

// UserSkillChecker 用户技能启用状态检查函数类型
// 通过函数注入，避免 common 层依赖 service/dao 层
type UserSkillChecker func(userName, skillCode string) bool
