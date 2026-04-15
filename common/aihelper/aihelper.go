package aihelper

import (
	"GopherAI/common/rabbitmq"
	"GopherAI/common/skill"
	"GopherAI/config"
	"GopherAI/model"
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"
)

// AIHelper AI助手结构体，包含消息历史、AI模型和多层记忆系统
type AIHelper struct {
	model    AIModel
	messages []*model.Message
	mu       sync.RWMutex
	SessionID string
	saveFunc  func(*model.Message) (*model.Message, error)

	summary       *SummaryMemory
	longTermMem   *LongTermMemoryManager
	userName      string
	messageCount  int // 本次会话累计消息数，用于触发长期记忆提取
}

// NewAIHelper 创建新的AIHelper实例
func NewAIHelper(model_ AIModel, SessionID string) *AIHelper {
	return &AIHelper{
		model:    model_,
		messages: make([]*model.Message, 0),
		saveFunc: func(msg *model.Message) (*model.Message, error) {
			data := rabbitmq.GenerateMessageMQParam(msg.SessionID, msg.Content, msg.UserName, msg.IsUser)
			err := rabbitmq.RMQMessage.Publish(data)
			return msg, err
		},
		SessionID: SessionID,
	}
}

// InitMemory 初始化记忆系统（在获取 userName 后调用）
func (a *AIHelper) InitMemory(userName string) {
	a.userName = userName
	a.summary = NewSummaryMemory(a.SessionID, userName)
	a.longTermMem = NewLongTermMemoryManager(userName)
	a.summary.LoadFromDB()
}

// addMessage 添加消息到内存中并调用自定义存储函数
func (a *AIHelper) AddMessage(Content string, UserName string, IsUser bool, Save bool) {
	userMsg := model.Message{
		SessionID: a.SessionID,
		Content:   Content,
		UserName:  UserName,
		IsUser:    IsUser,
	}
	a.messages = append(a.messages, &userMsg)
	a.messageCount++
	if Save {
		a.saveFunc(&userMsg)
	}
}

// SaveMessage 保存消息到数据库（通过回调函数避免循环依赖）
func (a *AIHelper) SetSaveFunc(saveFunc func(*model.Message) (*model.Message, error)) {
	a.saveFunc = saveFunc
}

// GetMessages 获取所有消息历史
func (a *AIHelper) GetMessages() []*model.Message {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*model.Message, len(a.messages))
	copy(out, a.messages)
	return out
}

// trySkill 检测 /skill 命令并执行，返回 (结果消息, 是否命中)
func (a *AIHelper) trySkill(ctx context.Context, userName, userQuestion string) (*model.Message, bool) {
	if !skill.IsSkillCommand(userQuestion) {
		return nil, false
	}

	code, args, ok := skill.ParseCommand(userQuestion)
	if !ok {
		return nil, false
	}

	registry := skill.GetRegistry()
	s, exists := registry.Get(code)
	if !exists {
		msg := &model.Message{
			SessionID: a.SessionID,
			UserName:  userName,
			Content:   fmt.Sprintf("技能 [%s] 不存在，可通过 GET /api/v1/skill/list 查看可用技能列表。", code),
			IsUser:    false,
		}
		return msg, true
	}

	invoker := skill.GetInvoker()
	if !invoker.IsEnabledForUser(userName, code) {
		msg := &model.Message{
			SessionID: a.SessionID,
			UserName:  userName,
			Content:   fmt.Sprintf("技能 [%s] 未启用，请先在技能管理中启用该技能。", code),
			IsUser:    false,
		}
		return msg, true
	}

	req := &skill.ExecuteRequest{
		UserName:  userName,
		SessionID: a.SessionID,
		RawInput:  userQuestion,
		Args:      args,
	}

	result, err := invoker.Invoke(ctx, s, req)
	if err != nil {
		msg := &model.Message{
			SessionID: a.SessionID,
			UserName:  userName,
			Content:   fmt.Sprintf("技能 [%s] 执行失败：%v", code, err),
			IsUser:    false,
		}
		return msg, true
	}

	msg := &model.Message{
		SessionID: a.SessionID,
		UserName:  userName,
		Content:   result.Output,
		IsUser:    false,
	}
	return msg, true
}

// buildContextMessages 使用三层记忆系统组装上下文
func (a *AIHelper) buildContextMessages() []*schema.Message {
	budget := GetContextTokenBudget()

	// 如果记忆系统已初始化，使用三层上下文组装
	if a.summary != nil {
		// 异步触发摘要生成（如果需要的话）
		if a.summary.shouldSummarize(a.messages, budget) {
			if adapter, ok := a.model.(SummaryLLM); ok {
				a.summary.TrySummarize(context.Background(), a.messages, budget, adapter)
			}
		}

		systemPrompt := config.GetConfig().MemoryConfig.SystemPrompt
		longTermMemory := ""
		if a.longTermMem != nil {
			longTermMemory = a.longTermMem.GetFormattedMemory()
		}

		return a.summary.BuildContext(a.messages, systemPrompt, longTermMemory, budget)
	}

	// 回退：无记忆系统时使用旧的截断逻辑
	contextMsgs := a.messages
	if budget > 0 {
		contextMsgs = TruncateByTokenBudget(a.messages, budget)
	}

	// 即使没有记忆系统，也尝试注入 System Prompt
	systemPrompt := config.GetConfig().MemoryConfig.SystemPrompt
	if systemPrompt != "" {
		result := make([]*schema.Message, 0, len(contextMsgs)+1)
		result = append(result, &schema.Message{Role: schema.System, Content: systemPrompt})
		for _, m := range contextMsgs {
			role := schema.Assistant
			if m.IsUser {
				role = schema.User
			}
			result = append(result, &schema.Message{Role: role, Content: m.Content})
		}
		return result
	}

	schemaMsgs := make([]*schema.Message, 0, len(contextMsgs))
	for _, m := range contextMsgs {
		role := schema.Assistant
		if m.IsUser {
			role = schema.User
		}
		schemaMsgs = append(schemaMsgs, &schema.Message{Role: role, Content: m.Content})
	}
	return schemaMsgs
}

// triggerLongTermExtraction 定期触发长期记忆提取（每 20 条消息提取一次）
func (a *AIHelper) triggerLongTermExtraction() {
	if a.longTermMem == nil {
		return
	}
	if a.messageCount > 0 && a.messageCount%20 == 0 {
		if adapter, ok := a.model.(SummaryLLM); ok {
			a.mu.RLock()
			msgs := make([]*model.Message, len(a.messages))
			copy(msgs, a.messages)
			a.mu.RUnlock()
			a.longTermMem.ExtractAndStore(context.Background(), msgs, a.SessionID, adapter)
		}
	}
}

// GenerateResponse 同步生成
func (a *AIHelper) GenerateResponse(userName string, ctx context.Context, userQuestion string) (*model.Message, error) {
	if skillMsg, handled := a.trySkill(ctx, userName, userQuestion); handled {
		a.AddMessage(userQuestion, userName, true, true)
		a.AddMessage(skillMsg.Content, userName, false, true)
		return skillMsg, nil
	}

	a.AddMessage(userQuestion, userName, true, true)

	a.mu.RLock()
	messages := a.buildContextMessages()
	a.mu.RUnlock()

	schemaMsg, err := a.model.GenerateResponse(ctx, messages)
	if err != nil {
		return nil, err
	}

	modelMsg := &model.Message{
		SessionID: a.SessionID,
		UserName:  userName,
		Content:   schemaMsg.Content,
		IsUser:    false,
	}

	a.AddMessage(modelMsg.Content, userName, false, true)
	a.triggerLongTermExtraction()

	return modelMsg, nil
}

// StreamResponse 流式生成
func (a *AIHelper) StreamResponse(userName string, ctx context.Context, cb StreamCallback, userQuestion string) (*model.Message, error) {
	if skillMsg, handled := a.trySkill(ctx, userName, userQuestion); handled {
		a.AddMessage(userQuestion, userName, true, true)
		a.AddMessage(skillMsg.Content, userName, false, true)
		cb(strings.ReplaceAll(skillMsg.Content, "\n", "<br>"))
		return skillMsg, nil
	}

	a.AddMessage(userQuestion, userName, true, true)

	a.mu.RLock()
	messages := a.buildContextMessages()
	a.mu.RUnlock()

	content, err := a.model.StreamResponse(ctx, messages, cb)
	if err != nil {
		return nil, err
	}

	modelMsg := &model.Message{
		SessionID: a.SessionID,
		UserName:  userName,
		Content:   content,
		IsUser:    false,
	}

	a.AddMessage(modelMsg.Content, userName, false, true)
	a.triggerLongTermExtraction()

	return modelMsg, nil
}

// GetModelType 获取模型类型
func (a *AIHelper) GetModelType() string {
	return a.model.GetModelType()
}
