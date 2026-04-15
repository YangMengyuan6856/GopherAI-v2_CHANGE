package aihelper

import (
	"GopherAI/common/skill"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
)

// ToolSource 工具来源抽象接口，ReAct 引擎通过此接口发现和执行工具
type ToolSource interface {
	SourceName() string
	DiscoverTools(ctx context.Context) ([]skill.ToolInfo, error)
	ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error)
}

// =================== MCP 工具源 ===================

// MCPToolSource 通过 MCP Server 发现和执行远程工具
type MCPToolSource struct {
	helper *skill.MCPHelper
}

func NewMCPToolSource(mcpBaseURL string) *MCPToolSource {
	return &MCPToolSource{helper: skill.NewMCPHelper(mcpBaseURL)}
}

func (m *MCPToolSource) SourceName() string { return "MCP" }

func (m *MCPToolSource) DiscoverTools(ctx context.Context) ([]skill.ToolInfo, error) {
	tools, err := m.helper.DiscoverTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("[MCP] discover tools failed: %w", err)
	}
	return tools, nil
}

func (m *MCPToolSource) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	calls := []skill.ToolCall{{Name: toolName, Arguments: args}}
	results := m.helper.ExecuteToolCalls(ctx, calls)
	if len(results) == 0 {
		return "", fmt.Errorf("MCP 工具 %q 无返回结果", toolName)
	}
	r := results[0]
	if !r.Success {
		return "", fmt.Errorf("MCP 工具 %q 调用失败: %s", toolName, r.Error)
	}
	return r.Output, nil
}

// =================== 本地 Skill 工具源 ===================

// skillExcludeSet 需要从 ReAct 工具列表中排除的技能（防止递归调用等）
var skillExcludeSet = map[string]bool{
	"agent": true,
}

// SkillToolSource 将 skill.Registry 中注册的本地技能暴露给 ReAct
type SkillToolSource struct {
	userName  string
	sessionID string
}

func NewSkillToolSource(userName, sessionID string) *SkillToolSource {
	return &SkillToolSource{userName: userName, sessionID: sessionID}
}

func (s *SkillToolSource) SourceName() string { return "Skill" }

func (s *SkillToolSource) DiscoverTools(_ context.Context) ([]skill.ToolInfo, error) {
	registry := skill.GetRegistry()
	allSkills := registry.All()

	tools := make([]skill.ToolInfo, 0, len(allSkills))
	for _, sk := range allSkills {
		if skillExcludeSet[sk.Code()] {
			continue
		}
		info := skill.ToolInfo{
			Name:        "skill_" + sk.Code(),
			Description: fmt.Sprintf("[本地技能] %s - %s", sk.Name(), sk.Description()),
			Parameters:  buildSkillParams(sk.Code()),
			Required:    buildSkillRequired(sk.Code()),
		}
		tools = append(tools, info)
	}
	return tools, nil
}

func (s *SkillToolSource) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	skillCode := strings.TrimPrefix(toolName, "skill_")

	registry := skill.GetRegistry()
	sk, exists := registry.Get(skillCode)
	if !exists {
		return "", fmt.Errorf("本地技能 %q 不存在", skillCode)
	}

	strArgs := make(map[string]string)
	for k, v := range args {
		strArgs[k] = fmt.Sprintf("%v", v)
	}

	req := &skill.ExecuteRequest{
		UserName:  s.userName,
		SessionID: s.sessionID,
		RawInput:  fmt.Sprintf("/skill %s %s", skillCode, argsToString(args)),
		Args:      strArgs,
	}

	invoker := skill.GetInvoker()
	result, err := invoker.Invoke(ctx, sk, req)
	if err != nil {
		return "", fmt.Errorf("技能 %q 执行失败: %w", skillCode, err)
	}
	return result.Output, nil
}

// buildSkillParams 根据技能代码返回该技能接受的参数描述
func buildSkillParams(code string) map[string]string {
	switch code {
	case "weather":
		return map[string]string{"city": "城市名称，如 北京、Shanghai"}
	case "calculator":
		return map[string]string{"query": "数学表达式，如 (1+2)*3"}
	case "translate":
		return map[string]string{"query": "要翻译的文本（中英文互译）"}
	case "summarize":
		return map[string]string{"query": "要生成摘要的长文本"}
	case "rag_query":
		return map[string]string{"query": "要在用户知识库中检索的问题"}
	case "datetime":
		return map[string]string{}
	default:
		return map[string]string{"query": "输入内容"}
	}
}

// buildSkillRequired 根据技能代码返回必填参数列表
func buildSkillRequired(code string) []string {
	switch code {
	case "weather":
		return []string{"city"}
	case "calculator", "translate", "summarize", "rag_query":
		return []string{"query"}
	case "datetime":
		return nil
	default:
		return []string{"query"}
	}
}

func argsToString(args map[string]interface{}) string {
	parts := make([]string, 0, len(args))
	for k, v := range args {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(parts, " ")
}

// =================== 自定义动态工具源 ===================

// CustomToolFunc 自定义工具的执行函数签名
type CustomToolFunc func(ctx context.Context, args map[string]interface{}) (string, error)

// CustomTool 一个动态注册的自定义工具
type CustomTool struct {
	Info    skill.ToolInfo
	Handler CustomToolFunc
}

// CustomToolSource 支持运行时动态注册/注销工具
type CustomToolSource struct {
	mu    sync.RWMutex
	tools map[string]*CustomTool
}

func NewCustomToolSource() *CustomToolSource {
	return &CustomToolSource{tools: make(map[string]*CustomTool)}
}

func (c *CustomToolSource) SourceName() string { return "Custom" }

// RegisterTool 动态注册一个自定义工具
func (c *CustomToolSource) RegisterTool(name, description string, params map[string]string, required []string, handler CustomToolFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tools[name] = &CustomTool{
		Info: skill.ToolInfo{
			Name:        name,
			Description: description,
			Parameters:  params,
			Required:    required,
		},
		Handler: handler,
	}
	log.Printf("[CustomToolSource] registered tool: %s", name)
}

// UnregisterTool 动态注销一个自定义工具
func (c *CustomToolSource) UnregisterTool(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tools, name)
	log.Printf("[CustomToolSource] unregistered tool: %s", name)
}

func (c *CustomToolSource) DiscoverTools(_ context.Context) ([]skill.ToolInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	tools := make([]skill.ToolInfo, 0, len(c.tools))
	for _, t := range c.tools {
		tools = append(tools, t.Info)
	}
	return tools, nil
}

func (c *CustomToolSource) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	c.mu.RLock()
	tool, exists := c.tools[toolName]
	c.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("自定义工具 %q 不存在", toolName)
	}
	return tool.Handler(ctx, args)
}

// =================== 工具聚合器 ===================

// ToolAggregator 聚合多个 ToolSource，提供统一的发现与执行入口
type ToolAggregator struct {
	sources  []ToolSource
	routeMap map[string]ToolSource // toolName -> source, 在 DiscoverAll 后填充
	mu       sync.RWMutex
}

func NewToolAggregator(sources ...ToolSource) *ToolAggregator {
	return &ToolAggregator{
		sources:  sources,
		routeMap: make(map[string]ToolSource),
	}
}

// DiscoverAll 从所有来源发现工具并构建路由映射
func (a *ToolAggregator) DiscoverAll(ctx context.Context) ([]skill.ToolInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	var allTools []skill.ToolInfo
	a.routeMap = make(map[string]ToolSource)

	for _, src := range a.sources {
		tools, err := src.DiscoverTools(ctx)
		if err != nil {
			log.Printf("[ToolAggregator] source %q discover failed: %v", src.SourceName(), err)
			continue
		}
		for _, t := range tools {
			if _, dup := a.routeMap[t.Name]; dup {
				log.Printf("[ToolAggregator] duplicate tool name %q from %q, skipping", t.Name, src.SourceName())
				continue
			}
			a.routeMap[t.Name] = src
			allTools = append(allTools, t)
		}
		log.Printf("[ToolAggregator] source %q provided %d tools", src.SourceName(), len(tools))
	}

	log.Printf("[ToolAggregator] total available tools: %d", len(allTools))
	return allTools, nil
}

// Execute 根据工具名路由到正确的 ToolSource 执行
func (a *ToolAggregator) Execute(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	a.mu.RLock()
	src, exists := a.routeMap[toolName]
	a.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("工具 %q 未在任何来源中注册", toolName)
	}

	log.Printf("[ToolAggregator] routing %q to source %q", toolName, src.SourceName())
	return src.ExecuteTool(ctx, toolName, args)
}

// AddSource 运行时追加新的工具来源
func (a *ToolAggregator) AddSource(src ToolSource) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sources = append(a.sources, src)
}
