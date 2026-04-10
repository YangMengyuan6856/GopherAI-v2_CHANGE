package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

const AgentSkillCode = "agent"

// AgentSkill 自主编排 MCP 工具的智能 Agent 技能
// 流程: 发现工具 → LLM 规划调用 → 执行 MCP 工具 → LLM 综合回答
type AgentSkill struct {
	mcpHelper *MCPHelper
}

func NewAgentSkill(mcpBaseURL string) *AgentSkill {
	return &AgentSkill{
		mcpHelper: NewMCPHelper(mcpBaseURL),
	}
}

func (a *AgentSkill) Code() string        { return AgentSkillCode }
func (a *AgentSkill) Name() string        { return "智能 Agent" }
func (a *AgentSkill) Description() string {
	return "自主调用 MCP 工具解决复杂问题，示例：/skill agent 查一下北京天气并算一下 380*5"
}

// llmPlanResponse LLM 规划阶段的结构化返回
type llmPlanResponse struct {
	ToolCalls    []ToolCall `json:"tool_calls"`
	DirectAnswer string     `json:"direct_answer"`
}

func (a *AgentSkill) Execute(ctx context.Context, req *ExecuteRequest) (*ExecuteResult, error) {
	query := req.Args["query"]
	if query == "" {
		return &ExecuteResult{
			SkillCode: AgentSkillCode,
			Output:    "请描述你的问题，示例：/skill agent 帮我查北京天气，再算一下 200*7 的费用",
		}, nil
	}

	// ── Step 1: 发现可用 MCP 工具 ──
	log.Printf("[Agent] Step 1: discovering MCP tools...")
	tools, err := a.mcpHelper.DiscoverTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("MCP 工具发现失败: %w", err)
	}
	log.Printf("[Agent] discovered %d tools: %v", len(tools), toolNames(tools))

	if len(tools) == 0 {
		answer, err := callLLMForSkill(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("LLM 回答失败: %w", err)
		}
		return &ExecuteResult{
			SkillCode: AgentSkillCode,
			Output:    answer,
		}, nil
	}

	// ── Step 2: LLM 规划 —— 决定调用哪些工具 ──
	log.Printf("[Agent] Step 2: LLM planning tool calls...")
	plan, err := a.planToolCalls(ctx, query, tools)
	if err != nil {
		return nil, fmt.Errorf("LLM 规划失败: %w", err)
	}

	if len(plan.ToolCalls) == 0 && plan.DirectAnswer != "" {
		log.Printf("[Agent] LLM decided no tools needed, direct answer")
		return &ExecuteResult{
			SkillCode: AgentSkillCode,
			Output:    plan.DirectAnswer,
			Data:      map[string]interface{}{"mode": "direct", "tools_used": 0},
		}, nil
	}

	// ── Step 3: 执行 MCP 工具调用 ──
	log.Printf("[Agent] Step 3: executing %d tool calls...", len(plan.ToolCalls))
	toolResults := a.mcpHelper.ExecuteToolCalls(ctx, plan.ToolCalls)

	for _, r := range toolResults {
		if r.Success {
			log.Printf("[Agent] tool %s: success", r.ToolName)
		} else {
			log.Printf("[Agent] tool %s: failed - %s", r.ToolName, r.Error)
		}
	}

	// ── Step 4: LLM 综合回答 ──
	log.Printf("[Agent] Step 4: LLM synthesizing final answer...")
	finalAnswer, err := a.synthesize(ctx, query, toolResults)
	if err != nil {
		return nil, fmt.Errorf("LLM 综合回答失败: %w", err)
	}

	usedTools := make([]string, 0)
	for _, r := range toolResults {
		usedTools = append(usedTools, r.ToolName)
	}

	return &ExecuteResult{
		SkillCode: AgentSkillCode,
		Output:    finalAnswer,
		Data: map[string]interface{}{
			"mode":         "agent",
			"tools_used":   len(plan.ToolCalls),
			"tool_names":   usedTools,
			"tool_results": toolResults,
		},
	}, nil
}

// planToolCalls 让 LLM 根据用户问题和可用工具决定调用计划
func (a *AgentSkill) planToolCalls(ctx context.Context, query string, tools []ToolInfo) (*llmPlanResponse, error) {
	toolDesc := FormatToolsForLLM(tools)

	prompt := fmt.Sprintf(`你是一个智能助手，拥有以下外部工具可以调用：

%s
请分析用户的问题，判断是否需要调用工具来获取实时信息或执行计算。

规则：
1. 如果问题需要实时数据（天气、时间等）或精确计算，请选择合适的工具
2. 如果问题不需要任何工具就能回答，请直接回答
3. 可以同时调用多个工具

请严格按以下 JSON 格式返回（不要包含任何其他文字，只返回 JSON）：

需要调用工具时：
{"tool_calls": [{"name": "工具名", "arguments": {"参数名": "参数值"}}], "direct_answer": ""}

不需要工具时：
{"tool_calls": [], "direct_answer": "你的回答"}

用户问题：%s`, toolDesc, query)

	result, err := callLLMForSkill(ctx, prompt)
	if err != nil {
		return nil, err
	}

	result = cleanJSONResponse(result)

	var plan llmPlanResponse
	if err := json.Unmarshal([]byte(result), &plan); err != nil {
		log.Printf("[Agent] LLM plan parse failed, raw: %s", result)
		return &llmPlanResponse{
			DirectAnswer: result,
		}, nil
	}

	validCalls := make([]ToolCall, 0)
	toolSet := make(map[string]bool)
	for _, t := range tools {
		toolSet[t.Name] = true
	}
	for _, c := range plan.ToolCalls {
		if toolSet[c.Name] {
			validCalls = append(validCalls, c)
		} else {
			log.Printf("[Agent] LLM requested unknown tool %q, skipping", c.Name)
		}
	}
	plan.ToolCalls = validCalls

	return &plan, nil
}

// synthesize 将工具执行结果与原始问题交给 LLM 生成最终回答
func (a *AgentSkill) synthesize(ctx context.Context, query string, results []ToolCallResult) (string, error) {
	var sb strings.Builder
	for _, r := range results {
		sb.WriteString(fmt.Sprintf("【工具: %s】\n", r.ToolName))
		if r.Success {
			sb.WriteString(r.Output)
		} else {
			sb.WriteString(fmt.Sprintf("调用失败: %s", r.Error))
		}
		sb.WriteString("\n\n")
	}

	prompt := fmt.Sprintf(`用户的问题是：%s

我已经通过工具获取了以下信息：

%s
请根据以上工具返回的真实数据，给用户一个完整、有条理的回答。
要求：
1. 直接基于工具返回的数据回答，不要编造数据
2. 用自然语言组织，不要简单复述工具输出
3. 如果有多个工具结果，请整合在一起回答`, query, sb.String())

	return callLLMForSkill(ctx, prompt)
}

// cleanJSONResponse 从 LLM 返回中提取 JSON 内容（处理 markdown 代码块包裹）
func cleanJSONResponse(raw string) string {
	raw = strings.TrimSpace(raw)

	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		start, end := 0, len(lines)
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				if start == 0 {
					start = i + 1
				} else {
					end = i
					break
				}
			}
		}
		if start > 0 && start < end {
			raw = strings.Join(lines[start:end], "\n")
		}
	}

	raw = strings.TrimSpace(raw)

	braceStart := strings.Index(raw, "{")
	braceEnd := strings.LastIndex(raw, "}")
	if braceStart >= 0 && braceEnd > braceStart {
		raw = raw[braceStart : braceEnd+1]
	}

	return raw
}

func toolNames(tools []ToolInfo) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}
