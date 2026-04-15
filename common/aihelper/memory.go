package aihelper

import (
	"GopherAI/config"
	"GopherAI/dao/memory"
	"GopherAI/model"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"
)

// SummaryMemory 会话摘要记忆，负责摘要生成、缓存和三层上下文组装
type SummaryMemory struct {
	sessionID string
	userName  string

	mu               sync.RWMutex
	cachedSummary    string // 当前缓存的摘要文本
	summarizedUpTo   int    // 摘要覆盖到的消息索引（不含），之后的是"工作记忆"
	summaryTokens    int    // 摘要本身的 token 数
	isSummarizing    bool   // 防止并发摘要
}

// NewSummaryMemory 创建摘要记忆实例
func NewSummaryMemory(sessionID, userName string) *SummaryMemory {
	return &SummaryMemory{
		sessionID: sessionID,
		userName:  userName,
	}
}

// LoadFromDB 从数据库加载已有摘要（启动恢复时使用）
func (sm *SummaryMemory) LoadFromDB() {
	summary, err := memory.GetSummaryBySessionID(sm.sessionID)
	if err != nil {
		return
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.cachedSummary = summary.Summary
	sm.summaryTokens = summary.TokenCount
}

// SetSummary 手动设置摘要（恢复历史时使用）
func (sm *SummaryMemory) SetSummary(text string, summarizedUpTo int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.cachedSummary = text
	sm.summarizedUpTo = summarizedUpTo
	sm.summaryTokens = EstimateTokenCount(text)
}

// GetSummary 获取当前摘要
func (sm *SummaryMemory) GetSummary() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.cachedSummary
}

// shouldSummarize 判断是否需要生成/更新摘要
func (sm *SummaryMemory) shouldSummarize(messages []*model.Message, budget int) bool {
	if budget <= 0 {
		return false
	}
	conf := config.GetConfig().MemoryConfig
	if !conf.EnableSummary {
		return false
	}

	totalTokens := 0
	for _, msg := range messages {
		totalTokens += EstimateMessageTokens(msg)
	}

	ratio := conf.SummaryTriggerRatio
	if ratio <= 0 {
		ratio = 0.7
	}
	return float64(totalTokens) > float64(budget)*ratio
}

// TrySummarize 在需要时异步生成摘要。调用者应先检查 shouldSummarize。
// 使用传入的 LLM 生成摘要，将被截断的旧消息压缩为摘要文本。
func (sm *SummaryMemory) TrySummarize(ctx context.Context, messages []*model.Message, budget int, llm SummaryLLM) {
	sm.mu.Lock()
	if sm.isSummarizing {
		sm.mu.Unlock()
		return
	}
	sm.isSummarizing = true
	sm.mu.Unlock()

	go func() {
		defer func() {
			sm.mu.Lock()
			sm.isSummarizing = false
			sm.mu.Unlock()
		}()

		recentMessages := TruncateByTokenBudget(messages, budget)
		recentStart := len(messages) - len(recentMessages)

		if recentStart <= sm.summarizedUpTo {
			return
		}

		// 要被摘要的消息：从上次摘要截止到本次工作记忆起点
		toSummarize := messages[sm.summarizedUpTo:recentStart]
		if len(toSummarize) == 0 {
			return
		}

		newSummary, err := sm.generateSummary(ctx, toSummarize, llm)
		if err != nil {
			log.Printf("[SummaryMemory] failed to generate summary: %v", err)
			return
		}

		sm.mu.Lock()
		sm.cachedSummary = newSummary
		sm.summarizedUpTo = recentStart
		sm.summaryTokens = EstimateTokenCount(newSummary)
		sm.mu.Unlock()

		sm.persistSummary(newSummary)
		log.Printf("[SummaryMemory] session=%s summarized %d messages, summary tokens=%d",
			sm.sessionID, len(toSummarize), sm.summaryTokens)
	}()
}

// generateSummary 调用 LLM 生成摘要
func (sm *SummaryMemory) generateSummary(ctx context.Context, messages []*model.Message, llm SummaryLLM) (string, error) {
	var sb strings.Builder
	existing := sm.GetSummary()
	if existing != "" {
		sb.WriteString("已有摘要：\n")
		sb.WriteString(existing)
		sb.WriteString("\n\n新增对话：\n")
	}
	for _, msg := range messages {
		role := "助手"
		if msg.IsUser {
			role = "用户"
		}
		sb.WriteString(fmt.Sprintf("%s: %s\n", role, msg.Content))
	}

	maxTokens := config.GetConfig().MemoryConfig.SummaryMaxTokens
	if maxTokens <= 0 {
		maxTokens = 500
	}

	prompt := fmt.Sprintf(`请将以下对话历史压缩为一段简洁的摘要，保留：
1. 用户提出的核心问题和需求
2. 已达成的结论和决策
3. 重要的上下文信息（如用户偏好、约束条件）

%s

请用中文输出摘要，控制在 %d 字以内。只输出摘要内容，不要添加额外说明。`, sb.String(), maxTokens)

	summaryMessages := []*schema.Message{
		{Role: schema.User, Content: prompt},
	}

	resp, err := llm.GenerateForSummary(ctx, summaryMessages)
	if err != nil {
		return "", fmt.Errorf("summary LLM call failed: %w", err)
	}
	return resp, nil
}

// persistSummary 将摘要持久化到数据库
func (sm *SummaryMemory) persistSummary(summaryText string) {
	entry := &model.ConversationSummary{
		SessionID:  sm.sessionID,
		UserName:   sm.userName,
		Summary:    summaryText,
		TokenCount: EstimateTokenCount(summaryText),
	}
	if err := memory.UpsertSummary(entry); err != nil {
		log.Printf("[SummaryMemory] persist summary failed: %v", err)
	}
}

// BuildContext 组装三层上下文：System Prompt + 摘要 + 最近的工作记忆
// 返回可直接传给 LLM 的 []*schema.Message
func (sm *SummaryMemory) BuildContext(messages []*model.Message, systemPrompt string, longTermMemory string, budget int) []*schema.Message {
	conf := config.GetConfig().MemoryConfig
	result := make([]*schema.Message, 0, len(messages)+3)

	// Layer 1: System Prompt（含长期记忆）
	sysContent := sm.buildSystemPromptContent(systemPrompt, longTermMemory)
	if sysContent != "" {
		result = append(result, &schema.Message{
			Role:    schema.System,
			Content: sysContent,
		})
	}

	// Layer 2: 摘要记忆
	sm.mu.RLock()
	summary := sm.cachedSummary
	sm.mu.RUnlock()

	if conf.EnableSummary && summary != "" {
		result = append(result, &schema.Message{
			Role:    schema.System,
			Content: fmt.Sprintf("[对话历史摘要]\n%s", summary),
		})
	}

	// Layer 3: 工作记忆（最近的原始消息，按 token 预算截取）
	workingMessages := messages
	if budget > 0 {
		consumed := 0
		if sysContent != "" {
			consumed += EstimateTokenCount(sysContent) + perMessageOverhead
		}
		if summary != "" {
			consumed += EstimateTokenCount(summary) + perMessageOverhead
		}
		remainingBudget := budget - consumed
		if remainingBudget < 200 {
			remainingBudget = 200
		}
		workingMessages = TruncateByTokenBudget(messages, remainingBudget)
	}

	for _, m := range workingMessages {
		role := schema.Assistant
		if m.IsUser {
			role = schema.User
		}
		result = append(result, &schema.Message{
			Role:    role,
			Content: m.Content,
		})
	}

	return result
}

// buildSystemPromptContent 构建系统提示内容
func (sm *SummaryMemory) buildSystemPromptContent(basePrompt string, longTermMemory string) string {
	if basePrompt == "" && longTermMemory == "" {
		return ""
	}
	var sb strings.Builder
	if basePrompt != "" {
		sb.WriteString(basePrompt)
	}
	if longTermMemory != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("[关于当前用户的长期记忆]\n")
		sb.WriteString(longTermMemory)
	}
	return sb.String()
}

// SummaryLLM 摘要生成所需的 LLM 接口（避免循环依赖）
type SummaryLLM interface {
	GenerateForSummary(ctx context.Context, messages []*schema.Message) (string, error)
}
