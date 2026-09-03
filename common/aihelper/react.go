package aihelper

import (
	"GopherAI/config"
	"GopherAI/internal/toolruntime"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ReActStep 记录一轮 Thought-Action-Observation
type ReActStep struct {
	Thought     string
	Action      string
	ActionInput map[string]interface{}
	Observation string
}

// ReActParsed 解析 LLM 单次输出的结果
type ReActParsed struct {
	Thought     string
	Action      string
	ActionInput map[string]interface{}
	FinalAnswer string
	IsFinal     bool
}

// ReActModel 基于 ReAct 循环的推理模型，实现 AIModel 接口。
// 通过 ToolAggregator 聚合 MCP 和动态注册工具。
type ReActModel struct {
	llm           model.ToolCallingChatModel
	aggregator    *ToolAggregator
	customSource  *CustomToolSource
	maxIterations int

	// 工具列表缓存，首次调用后填充
	toolsCache []toolruntime.ToolInfo
	toolsMu    sync.Mutex
}

// NewReActModel 创建 ReAct 推理模型，聚合 MCP 与动态注册工具。
func NewReActModel(ctx context.Context, _ string) (*ReActModel, error) {
	key := os.Getenv("OPENAI_API_KEY")
	conf := config.GetConfig()
	modelName := conf.RagModelConfig.RagChatModelName
	baseURL := conf.RagModelConfig.RagBaseUrl

	llm, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: baseURL,
		Model:   modelName,
		APIKey:  key,
	})
	if err != nil {
		return nil, fmt.Errorf("create react model failed: %v", err)
	}

	reactConf := conf.ReactConfig
	maxIter := reactConf.MaxIterations
	if maxIter <= 0 {
		maxIter = 10
	}
	mcpURL := reactConf.McpBaseURL
	if mcpURL == "" {
		mcpURL = "http://localhost:8081/mcp"
	}

	mcpSource := NewMCPToolSource(mcpURL)
	customSource := NewCustomToolSource()

	aggregator := NewToolAggregator(mcpSource, customSource)

	return &ReActModel{
		llm:           llm,
		aggregator:    aggregator,
		customSource:  customSource,
		maxIterations: maxIter,
	}, nil
}

// RegisterCustomTool 允许外部在运行时动态注册工具
func (r *ReActModel) RegisterCustomTool(name, description string, params map[string]string, required []string, handler CustomToolFunc) {
	r.customSource.RegisterTool(name, description, params, required, handler)
	// 清除缓存，下次调用时重新发现
	r.toolsMu.Lock()
	r.toolsCache = nil
	r.toolsMu.Unlock()
}

// discoverTools 首次调用时从所有来源发现工具并缓存
func (r *ReActModel) discoverTools(ctx context.Context) ([]toolruntime.ToolInfo, error) {
	r.toolsMu.Lock()
	defer r.toolsMu.Unlock()

	if r.toolsCache != nil {
		return r.toolsCache, nil
	}

	tools, err := r.aggregator.DiscoverAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover tools failed: %w", err)
	}
	r.toolsCache = tools
	return tools, nil
}

// buildSystemPrompt 构建包含所有工具描述和 ReAct 格式规范的 System Prompt
func (r *ReActModel) buildSystemPrompt(tools []toolruntime.ToolInfo) string {
	toolDesc := toolruntime.FormatToolsForLLM(tools)

	return fmt.Sprintf(`你是一个强大的智能助手，能够通过逐步推理和调用外部工具来解决复杂问题。
你可以使用经过系统注册的 MCP 远程工具和动态工具。

## 可用工具
%s
## 回答格式

你必须严格按照以下格式进行逐步推理：

Thought: <分析当前情况，思考下一步该做什么>
Action: <要调用的工具名称>
Action Input: <工具参数，JSON 格式>

工具执行后你会收到：
Observation: <工具返回的结果>

然后你可以继续思考和调用工具，直到你有足够的信息回答用户。
当你准备好给出最终答案时，使用：

Thought: <总结推理过程>
Final Answer: <给用户的完整回答>

## 重要规则
1. 每次只能调用一个工具
2. Action 必须是上面列出的工具名称之一
3. Action Input 必须是合法的 JSON 对象
4. 如果不需要调用任何工具就能回答，直接使用 Thought + Final Answer
5. 不要编造工具不存在的数据，如果工具调用失败请如实告知用户
6. 可以多次调用不同工具来组合解决复杂问题`, toolDesc)
}

// buildScratchpad 将已完成的推理步骤构建为 scratchpad 文本
func (r *ReActModel) buildScratchpad(steps []ReActStep) string {
	if len(steps) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, s := range steps {
		sb.WriteString(fmt.Sprintf("Thought: %s\n", s.Thought))
		if s.Action != "" {
			sb.WriteString(fmt.Sprintf("Action: %s\n", s.Action))
			inputJSON, _ := json.Marshal(s.ActionInput)
			sb.WriteString(fmt.Sprintf("Action Input: %s\n", string(inputJSON)))
			sb.WriteString(fmt.Sprintf("Observation: %s\n", s.Observation))
		}
	}
	return sb.String()
}

var (
	reThought     = regexp.MustCompile(`(?i)Thought\s*:\s*(.+?)(?:\n|$)`)
	reAction      = regexp.MustCompile(`(?i)Action\s*:\s*(.+?)(?:\n|$)`)
	reActionInput = regexp.MustCompile(`(?i)Action\s*Input\s*:\s*(.+)`)
	reFinalAnswer = regexp.MustCompile(`(?i)Final\s*Answer\s*:\s*(.+)`)
)

// parseReActOutput 解析 LLM 的单次输出
func parseReActOutput(text string) *ReActParsed {
	parsed := &ReActParsed{}

	if m := reThought.FindStringSubmatch(text); len(m) > 1 {
		parsed.Thought = strings.TrimSpace(m[1])
	}

	if m := reFinalAnswer.FindStringSubmatch(text); len(m) > 1 {
		parsed.IsFinal = true
		idx := reFinalAnswer.FindStringIndex(text)
		if idx != nil {
			afterLabel := text[idx[0]:]
			colonIdx := strings.Index(afterLabel, ":")
			if colonIdx >= 0 {
				parsed.FinalAnswer = strings.TrimSpace(afterLabel[colonIdx+1:])
			}
		}
		if parsed.FinalAnswer == "" {
			parsed.FinalAnswer = strings.TrimSpace(m[1])
		}
		return parsed
	}

	if m := reAction.FindStringSubmatch(text); len(m) > 1 {
		actionName := strings.TrimSpace(m[1])
		if !strings.HasPrefix(strings.ToLower(actionName), "input") {
			parsed.Action = actionName
		}
	}

	if m := reActionInput.FindStringSubmatch(text); len(m) > 1 {
		rawInput := strings.TrimSpace(m[1])
		idx := reActionInput.FindStringIndex(text)
		if idx != nil {
			remainder := text[idx[0]:]
			colonIdx := strings.Index(remainder, ":")
			if colonIdx >= 0 {
				rawInput = strings.TrimSpace(remainder[colonIdx+1:])
			}
		}
		rawInput = cleanJSONBlock(rawInput)

		var args map[string]interface{}
		if err := json.Unmarshal([]byte(rawInput), &args); err == nil {
			parsed.ActionInput = args
		} else {
			parsed.ActionInput = map[string]interface{}{"query": rawInput}
		}
	}

	return parsed
}

// cleanJSONBlock 去除 markdown 代码块包裹
func cleanJSONBlock(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		start, end := -1, len(lines)
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") {
				if start < 0 {
					start = i + 1
				} else {
					end = i
					break
				}
			}
		}
		if start >= 0 && start < end {
			s = strings.Join(lines[start:end], "\n")
		}
	}
	s = strings.TrimSpace(s)
	braceStart := strings.Index(s, "{")
	braceEnd := strings.LastIndex(s, "}")
	if braceStart >= 0 && braceEnd > braceStart {
		s = s[braceStart : braceEnd+1]
	}
	return s
}

// executeTool 通过 ToolAggregator 路由到正确的来源执行工具
func (r *ReActModel) executeTool(ctx context.Context, action string, input map[string]interface{}) string {
	result, err := r.aggregator.Execute(ctx, action, input)
	if err != nil {
		return fmt.Sprintf("工具调用失败: %v", err)
	}
	return result
}

// forceFinish 达到最大迭代次数后，强制让 LLM 根据已有信息生成最终回答
func (r *ReActModel) forceFinish(ctx context.Context, messages []*schema.Message, scratchpad string) (string, error) {
	forcePrompt := fmt.Sprintf(`你之前的推理过程如下：

%s

你已经达到了最大推理步数。请根据上面已经获得的所有信息，立即给出最终回答。
不要再调用任何工具，直接回答用户的问题。`, scratchpad)

	finalMessages := make([]*schema.Message, len(messages))
	copy(finalMessages, messages)
	finalMessages = append(finalMessages, &schema.Message{
		Role:    schema.User,
		Content: forcePrompt,
	})

	resp, err := r.llm.Generate(ctx, finalMessages)
	if err != nil {
		return "", fmt.Errorf("force finish generate failed: %v", err)
	}
	return resp.Content, nil
}

// GenerateResponse 同步 ReAct 循环
func (r *ReActModel) GenerateResponse(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	tools, err := r.discoverTools(ctx)
	if err != nil {
		log.Printf("[ReAct] tool discovery failed, falling back to direct LLM: %v", err)
		return r.llm.Generate(ctx, messages)
	}

	if len(tools) == 0 {
		return r.llm.Generate(ctx, messages)
	}

	systemPrompt := r.buildSystemPrompt(tools)

	reactMessages := make([]*schema.Message, 0, len(messages)+2)
	reactMessages = append(reactMessages, &schema.Message{
		Role:    schema.System,
		Content: systemPrompt,
	})
	reactMessages = append(reactMessages, messages...)

	var steps []ReActStep
	validToolSet := make(map[string]bool)
	for _, t := range tools {
		validToolSet[t.Name] = true
	}

	for i := 0; i < r.maxIterations; i++ {
		scratchpad := r.buildScratchpad(steps)
		var currentMessages []*schema.Message
		currentMessages = append(currentMessages, reactMessages...)
		if scratchpad != "" {
			currentMessages = append(currentMessages, &schema.Message{
				Role:    schema.Assistant,
				Content: scratchpad,
			})
		}

		resp, err := r.llm.Generate(ctx, currentMessages)
		if err != nil {
			return nil, fmt.Errorf("react iteration %d generate failed: %v", i+1, err)
		}

		log.Printf("[ReAct] iteration %d raw output: %s", i+1, resp.Content)
		parsed := parseReActOutput(resp.Content)

		if parsed.IsFinal {
			return &schema.Message{
				Role:    schema.Assistant,
				Content: parsed.FinalAnswer,
			}, nil
		}

		step := ReActStep{Thought: parsed.Thought}

		if parsed.Action != "" && validToolSet[parsed.Action] {
			step.Action = parsed.Action
			step.ActionInput = parsed.ActionInput
			log.Printf("[ReAct] iteration %d calling tool %q via aggregator", i+1, parsed.Action)
			observation := r.executeTool(ctx, parsed.Action, parsed.ActionInput)
			step.Observation = observation
			log.Printf("[ReAct] iteration %d observation: %s", i+1, observation)
		} else if parsed.Action != "" {
			step.Action = parsed.Action
			step.Observation = fmt.Sprintf("未知工具 %q，请使用可用工具列表中的工具", parsed.Action)
			log.Printf("[ReAct] iteration %d unknown tool: %s", i+1, parsed.Action)
		}

		steps = append(steps, step)
	}

	log.Printf("[ReAct] max iterations reached, forcing final answer")
	scratchpad := r.buildScratchpad(steps)
	answer, err := r.forceFinish(ctx, reactMessages, scratchpad)
	if err != nil {
		return nil, err
	}
	return &schema.Message{
		Role:    schema.Assistant,
		Content: answer,
	}, nil
}

// StreamResponse 流式 ReAct 循环，通过 SSE 推送中间推理过程
func (r *ReActModel) StreamResponse(ctx context.Context, messages []*schema.Message, cb StreamCallback) (string, error) {
	tools, err := r.discoverTools(ctx)
	if err != nil {
		log.Printf("[ReAct] tool discovery failed, falling back to direct LLM stream: %v", err)
		return r.directStream(ctx, messages, cb)
	}

	if len(tools) == 0 {
		return r.directStream(ctx, messages, cb)
	}

	systemPrompt := r.buildSystemPrompt(tools)

	reactMessages := make([]*schema.Message, 0, len(messages)+2)
	reactMessages = append(reactMessages, &schema.Message{
		Role:    schema.System,
		Content: systemPrompt,
	})
	reactMessages = append(reactMessages, messages...)

	var steps []ReActStep
	validToolSet := make(map[string]bool)
	for _, t := range tools {
		validToolSet[t.Name] = true
	}

	for i := 0; i < r.maxIterations; i++ {
		scratchpad := r.buildScratchpad(steps)
		var currentMessages []*schema.Message
		currentMessages = append(currentMessages, reactMessages...)
		if scratchpad != "" {
			currentMessages = append(currentMessages, &schema.Message{
				Role:    schema.Assistant,
				Content: scratchpad,
			})
		}

		resp, err := r.llm.Generate(ctx, currentMessages)
		if err != nil {
			return "", fmt.Errorf("react iteration %d generate failed: %v", i+1, err)
		}

		log.Printf("[ReAct] stream iteration %d raw output: %s", i+1, resp.Content)
		parsed := parseReActOutput(resp.Content)

		if parsed.Thought != "" {
			cb(fmt.Sprintf("\n\n**[思考]** %s\n\n", parsed.Thought))
		}

		if parsed.IsFinal {
			cb(parsed.FinalAnswer)
			return parsed.FinalAnswer, nil
		}

		step := ReActStep{Thought: parsed.Thought}

		if parsed.Action != "" && validToolSet[parsed.Action] {
			step.Action = parsed.Action
			step.ActionInput = parsed.ActionInput
			inputJSON, _ := json.Marshal(parsed.ActionInput)
			cb(fmt.Sprintf("**[调用工具]** %s(%s)\n\n", parsed.Action, string(inputJSON)))

			observation := r.executeTool(ctx, parsed.Action, parsed.ActionInput)
			step.Observation = observation
			cb(fmt.Sprintf("**[工具结果]** %s\n\n", observation))
		} else if parsed.Action != "" {
			step.Action = parsed.Action
			step.Observation = fmt.Sprintf("未知工具 %q，请使用可用工具列表中的工具", parsed.Action)
			cb(fmt.Sprintf("**[错误]** %s\n\n", step.Observation))
		}

		steps = append(steps, step)
	}

	log.Printf("[ReAct] stream max iterations reached, forcing final answer")
	cb("\n\n**[达到最大推理步数，正在生成最终回答...]**\n\n")

	scratchpad := r.buildScratchpad(steps)

	finalMessages := make([]*schema.Message, len(reactMessages))
	copy(finalMessages, reactMessages)
	finalMessages = append(finalMessages, &schema.Message{
		Role:    schema.User,
		Content: fmt.Sprintf("你之前的推理过程如下：\n\n%s\n\n请根据已有信息给出最终回答。", scratchpad),
	})

	stream, err := r.llm.Stream(ctx, finalMessages)
	if err != nil {
		answer, ferr := r.forceFinish(ctx, reactMessages, scratchpad)
		if ferr != nil {
			return "", ferr
		}
		cb(answer)
		return answer, nil
	}
	defer stream.Close()

	var finalResp strings.Builder
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if finalResp.Len() > 0 {
				return finalResp.String(), nil
			}
			answer, ferr := r.forceFinish(ctx, reactMessages, scratchpad)
			if ferr != nil {
				return "", ferr
			}
			cb(answer)
			return answer, nil
		}
		if len(msg.Content) > 0 {
			finalResp.WriteString(msg.Content)
			cb(msg.Content)
		}
	}

	result := finalResp.String()
	if result == "" {
		answer, ferr := r.forceFinish(ctx, reactMessages, scratchpad)
		if ferr != nil {
			return "", ferr
		}
		return answer, nil
	}
	return result, nil
}

// directStream 工具不可用时直接流式输出
func (r *ReActModel) directStream(ctx context.Context, messages []*schema.Message, cb StreamCallback) (string, error) {
	stream, err := r.llm.Stream(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("react direct stream failed: %v", err)
	}
	defer stream.Close()

	var fullResp strings.Builder
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("react direct stream recv failed: %v", err)
		}
		if len(msg.Content) > 0 {
			fullResp.WriteString(msg.Content)
			cb(msg.Content)
		}
	}
	return fullResp.String(), nil
}

func (r *ReActModel) GetModelType() string { return "5" }

func (r *ReActModel) GenerateForSummary(ctx context.Context, messages []*schema.Message) (string, error) {
	resp, err := r.llm.Generate(ctx, messages)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}
