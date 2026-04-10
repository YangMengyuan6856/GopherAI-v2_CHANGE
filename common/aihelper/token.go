package aihelper

import (
	"GopherAI/config"
	"GopherAI/model"
	"log"
	"unicode"
)

// perMessageOverhead 每条消息的固定开销（role 标记 + JSON 结构 ≈ 4 token）
const perMessageOverhead = 4

// EstimateTokenCount 估算一段文本的 token 数量。
//
// 原理：大模型使用 BPE 分词，精确计算需要加载分词器，但在服务端做粗估即可满足截断需求。
// 估算规则（偏保守，宁可多估不会少估）：
//   - 中文字符（CJK）：每个字 ≈ 1~2 token，这里取 2
//   - 英文/数字/符号（ASCII）：大约 3~4 个字符 ≈ 1 token
//
// 举例："你好世界Hello" → 中文4字×2 + 英文5字/4 ≈ 8+1 = 9 token（实际约 6~7）
func EstimateTokenCount(text string) int {
	if len(text) == 0 {
		return 0
	}
	cjkTokens := 0
	asciiChars := 0

	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			cjkTokens += 2
		} else {
			asciiChars++
		}
	}

	return cjkTokens + (asciiChars+3)/4
}

// EstimateMessageTokens 估算单条消息的 token 数（内容 + 消息结构开销）
func EstimateMessageTokens(msg *model.Message) int {
	return EstimateTokenCount(msg.Content) + perMessageOverhead
}

// TruncateByTokenBudget 按 token 预算从最新消息向前截取。
//
// 算法：
//  1. 从消息列表的末尾（最新的消息）开始，向前逐条累加 token
//  2. 当累计 token 超过 budget 时停止
//  3. 返回截取后的消息切片（保持原始顺序）
//  4. 至少保留最后一条消息（当前用户提问），即使它本身就超过预算
//
// 入参 messages 不会被修改，返回的是原切片的一个子切片。
func TruncateByTokenBudget(messages []*model.Message, budget int) []*model.Message {
	if budget <= 0 || len(messages) == 0 {
		return messages
	}

	// 从后往前扫描，累计 token 直到耗尽预算
	usedTokens := 0
	startIdx := len(messages) // 初始指向末尾之后

	for i := len(messages) - 1; i >= 0; i-- {
		msgTokens := EstimateMessageTokens(messages[i])
		if usedTokens+msgTokens > budget {
			break
		}
		usedTokens += msgTokens
		startIdx = i
	}

	// 保底：至少保留最后一条（当前用户提问不能丢）
	if startIdx >= len(messages) {
		startIdx = len(messages) - 1
	}

	kept := messages[startIdx:]
	dropped := len(messages) - len(kept)

	if dropped > 0 {
		log.Printf("[TokenBudget] truncated context: kept %d/%d messages, ~%d tokens (budget=%d, dropped=%d oldest)",
			len(kept), len(messages), usedTokens, budget, dropped)
	}

	return kept
}

// GetContextTokenBudget 从配置中读取上下文 token 预算
func GetContextTokenBudget() int {
	budget := config.GetConfig().MainConfig.ContextTokenBudget
	if budget <= 0 {
		return 0 // 0 表示不限制
	}
	return budget
}
