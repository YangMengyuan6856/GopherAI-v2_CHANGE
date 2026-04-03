package aihelper

import (
	"GopherAI/common/rabbitmq"
	"GopherAI/common/skill"
	"GopherAI/model"
	"GopherAI/utils"
	"context"
	"fmt"
	"sync"
)

// AIHelper AI助手结构体，包含消息历史和AI模型
type AIHelper struct {
	model    AIModel
	messages []*model.Message
	mu       sync.RWMutex
	//一个会话绑定一个AIHelper
	SessionID string
	saveFunc  func(*model.Message) (*model.Message, error)
}

// NewAIHelper 创建新的AIHelper实例
func NewAIHelper(model_ AIModel, SessionID string) *AIHelper {
	return &AIHelper{
		model:    model_,
		messages: make([]*model.Message, 0),
		//异步推送到消息队列中
		saveFunc: func(msg *model.Message) (*model.Message, error) {
			data := rabbitmq.GenerateMessageMQParam(msg.SessionID, msg.Content, msg.UserName, msg.IsUser)
			err := rabbitmq.RMQMessage.Publish(data)
			return msg, err
		},
		SessionID: SessionID,
	}
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
	if Save {
		a.saveFunc(&userMsg)
	}
}

// SaveMessage 保存消息到数据库（通过回调函数避免循环依赖）
// 通过传入func，自己调用外部的保存函数，即可支持同步异步等多种策略
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

	req := &skill.ExecuteRequest{
		UserName:  userName,
		SessionID: a.SessionID,
		RawInput:  userQuestion,
		Args:      args,
	}

	result, err := skill.GetInvoker().Invoke(ctx, s, req)
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

// 同步生成
func (a *AIHelper) GenerateResponse(userName string, ctx context.Context, userQuestion string) (*model.Message, error) {
	// 优先检测是否为技能命令（/skill <code> <args>）
	if skillMsg, handled := a.trySkill(ctx, userName, userQuestion); handled {
		a.AddMessage(userQuestion, userName, true, true)
		a.AddMessage(skillMsg.Content, userName, false, true)
		return skillMsg, nil
	}

	//调用存储函数
	a.AddMessage(userQuestion, userName, true, true)

	a.mu.RLock()
	//将model.Message转化成schema.Message
	messages := utils.ConvertToSchemaMessages(a.messages)
	a.mu.RUnlock()

	//调用模型生成回复
	schemaMsg, err := a.model.GenerateResponse(ctx, messages)
	if err != nil {
		return nil, err
	}

	//将schema.Message转化成model.Message
	modelMsg := utils.ConvertToModelMessage(a.SessionID, userName, schemaMsg)

	//调用存储函数
	a.AddMessage(modelMsg.Content, userName, false, true)

	return modelMsg, nil
}

// 流式生成
func (a *AIHelper) StreamResponse(userName string, ctx context.Context, cb StreamCallback, userQuestion string) (*model.Message, error) {
	// 优先检测是否为技能命令（/skill <code> <args>）
	if skillMsg, handled := a.trySkill(ctx, userName, userQuestion); handled {
		a.AddMessage(userQuestion, userName, true, true)
		a.AddMessage(skillMsg.Content, userName, false, true)
		// 技能结果通过流式回调一次性发送给前端
		cb(skillMsg.Content)
		return skillMsg, nil
	}

	//调用存储函数
	a.AddMessage(userQuestion, userName, true, true)

	a.mu.RLock()
	messages := utils.ConvertToSchemaMessages(a.messages)
	a.mu.RUnlock()

	content, err := a.model.StreamResponse(ctx, messages, cb)
	if err != nil {
		return nil, err
	}
	//转化成model.Message
	modelMsg := &model.Message{
		SessionID: a.SessionID,
		UserName:  userName,
		Content:   content,
		IsUser:    false,
	}

	//调用存储函数
	a.AddMessage(modelMsg.Content, userName, false, true)

	return modelMsg, nil
}

// GetModelType 获取模型类型
func (a *AIHelper) GetModelType() string {
	return a.model.GetModelType()
}
