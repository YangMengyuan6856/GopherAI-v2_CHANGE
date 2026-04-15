package aihelper

import (
	"GopherAI/config"
	"GopherAI/dao/memory"
	"GopherAI/model"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// LongTermMemoryManager 长期记忆管理器，负责提取、存储和注入跨会话的用户记忆
type LongTermMemoryManager struct {
	userName string
}

// NewLongTermMemoryManager 创建长期记忆管理器
func NewLongTermMemoryManager(userName string) *LongTermMemoryManager {
	return &LongTermMemoryManager{userName: userName}
}

// ExtractAndStore 从对话中提取值得长期记忆的信息，异步执行
func (ltm *LongTermMemoryManager) ExtractAndStore(ctx context.Context, messages []*model.Message, sessionID string, llm SummaryLLM) {
	conf := config.GetConfig().MemoryConfig
	if !conf.EnableLongTermMemory {
		return
	}
	if len(messages) < 4 {
		return
	}

	go func() {
		entries, err := ltm.extractMemories(ctx, messages, llm)
		if err != nil {
			log.Printf("[LongTermMemory] extraction failed: %v", err)
			return
		}
		if len(entries) == 0 {
			return
		}

		for _, entry := range entries {
			entry.UserName = ltm.userName
			entry.Source = sessionID
			if err := memory.CreateMemoryEntry(entry); err != nil {
				log.Printf("[LongTermMemory] save entry failed: %v", err)
			}
		}

		maxEntries := conf.LongTermMemoryMaxEntries
		if maxEntries <= 0 {
			maxEntries = 50
		}
		if err := memory.PruneOldestEntries(ltm.userName, maxEntries); err != nil {
			log.Printf("[LongTermMemory] prune failed: %v", err)
		}

		log.Printf("[LongTermMemory] user=%s extracted %d memory entries from session=%s",
			ltm.userName, len(entries), sessionID)
	}()
}

// extractMemories 调用 LLM 从对话中提取记忆条目
func (ltm *LongTermMemoryManager) extractMemories(ctx context.Context, messages []*model.Message, llm SummaryLLM) ([]*model.MemoryEntry, error) {
	var sb strings.Builder
	for _, msg := range messages {
		role := "助手"
		if msg.IsUser {
			role = "用户"
		}
		sb.WriteString(fmt.Sprintf("%s: %s\n", role, msg.Content))
	}

	prompt := fmt.Sprintf(`分析以下对话，提取值得长期记住的用户信息。

提取规则：
1. 只提取用户明确表达的事实性信息，不要推测
2. 类别分为三种：preference（偏好）、fact（事实）、instruction（指令/要求）
3. 每条记忆应简洁明了，一句话概括

以 JSON 数组格式返回，例如：
[{"category": "preference", "content": "用户喜欢简洁的代码风格"}]

如果没有值得记忆的信息，返回空数组 []

对话内容：
%s

请只输出 JSON 数组，不要添加任何额外说明或 markdown 格式。`, sb.String())

	extractMessages := []*schema.Message{
		{Role: schema.User, Content: prompt},
	}

	resp, err := llm.GenerateForSummary(ctx, extractMessages)
	if err != nil {
		return nil, fmt.Errorf("extraction LLM call failed: %w", err)
	}

	return parseMemoryEntries(resp)
}

// memoryEntryJSON LLM 返回的 JSON 结构
type memoryEntryJSON struct {
	Category string `json:"category"`
	Content  string `json:"content"`
}

// parseMemoryEntries 解析 LLM 返回的 JSON 记忆条目
func parseMemoryEntries(raw string) ([]*model.MemoryEntry, error) {
	raw = strings.TrimSpace(raw)
	// 去除可能的 markdown 代码块包裹
	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		var cleaned []string
		inBlock := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") {
				inBlock = !inBlock
				continue
			}
			if inBlock || !strings.HasPrefix(trimmed, "```") {
				cleaned = append(cleaned, line)
			}
		}
		raw = strings.Join(cleaned, "\n")
		raw = strings.TrimSpace(raw)
	}

	// 提取 JSON 数组
	startIdx := strings.Index(raw, "[")
	endIdx := strings.LastIndex(raw, "]")
	if startIdx >= 0 && endIdx > startIdx {
		raw = raw[startIdx : endIdx+1]
	}

	var entries []memoryEntryJSON
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("parse memory JSON failed: %w (raw=%s)", err, raw)
	}

	validCategories := map[string]bool{
		"preference":  true,
		"fact":        true,
		"instruction": true,
	}

	var result []*model.MemoryEntry
	for _, e := range entries {
		if e.Content == "" {
			continue
		}
		category := e.Category
		if !validCategories[category] {
			category = "fact"
		}
		result = append(result, &model.MemoryEntry{
			Category: category,
			Content:  e.Content,
		})
	}
	return result, nil
}

// GetFormattedMemory 获取格式化的长期记忆文本，用于注入 System Prompt
func (ltm *LongTermMemoryManager) GetFormattedMemory() string {
	conf := config.GetConfig().MemoryConfig
	if !conf.EnableLongTermMemory {
		return ""
	}

	maxEntries := conf.LongTermMemoryMaxEntries
	if maxEntries <= 0 {
		maxEntries = 50
	}

	entries, err := memory.GetMemoryEntriesByUserName(ltm.userName, maxEntries)
	if err != nil {
		log.Printf("[LongTermMemory] load entries failed: %v", err)
		return ""
	}
	if len(entries) == 0 {
		return ""
	}

	categoryNames := map[string]string{
		"preference":  "偏好",
		"fact":        "事实",
		"instruction": "指令",
	}

	grouped := make(map[string][]string)
	for _, e := range entries {
		grouped[e.Category] = append(grouped[e.Category], e.Content)
	}

	var sb strings.Builder
	for cat, items := range grouped {
		name := categoryNames[cat]
		if name == "" {
			name = cat
		}
		sb.WriteString(fmt.Sprintf("- %s：", name))
		sb.WriteString(strings.Join(items, "；"))
		sb.WriteString("\n")
	}
	return sb.String()
}
