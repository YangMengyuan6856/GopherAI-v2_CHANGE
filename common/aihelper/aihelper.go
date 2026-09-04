package aihelper

import (
	"GopherAI/config"
	memorydomain "GopherAI/internal/memory"
	"GopherAI/internal/observability"
	profiledomain "GopherAI/internal/profilememory"
	"GopherAI/model"
	"context"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
)

// AIHelper AI助手结构体，包含消息历史、AI模型和多层记忆系统
type AIHelper struct {
	model           AIModel
	messages        []*model.Message
	mu              sync.RWMutex
	SessionID       string
	saveFunc        func(context.Context, *model.Message) (*model.Message, error)
	historyLoaded   bool
	userName        string
	profileRecall   ProfileRecallService
	profileObserver ProfileRecallObserver
}

type ProfileRecallService interface {
	Recall(context.Context, string, string, string, int) (profiledomain.RecallResponse, error)
}

type ProfileRecallObserver interface {
	RecordProfileRecall(string, time.Duration, int)
}

// NewAIHelper 创建新的AIHelper实例
func NewAIHelper(model_ AIModel, SessionID string) *AIHelper {
	memoryService := memorydomain.NewDefaultService()
	return &AIHelper{
		model:    model_,
		messages: make([]*model.Message, 0),
		saveFunc: func(ctx context.Context, msg *model.Message) (*model.Message, error) {
			role := memorydomain.RoleAssistant
			if msg.IsUser {
				role = memorydomain.RoleUser
			}
			persisted, err := memoryService.AppendMessage(ctx, msg.UserName, msg.SessionID, role, msg.Content)
			if err != nil {
				return nil, err
			}
			msg.ID, msg.CreatedAt = persisted.ID, persisted.CreatedAt
			return msg, nil
		},
		SessionID: SessionID, profileRecall: profiledomain.NewDefaultService(), profileObserver: observability.DefaultMetrics(),
	}
}

// InitMemory binds the helper to an owner. Legacy free-form summary and
// periodic profile extraction are intentionally not activated: the v2 memory
// path only admits structured, source-aware memory.
func (a *AIHelper) InitMemory(userName string) {
	a.userName = userName
}

func (a *AIHelper) SetProfileRecall(service ProfileRecallService, observer ProfileRecallObserver) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.profileRecall, a.profileObserver = service, observer
}

// addMessage 添加消息到内存中并调用自定义存储函数
func (a *AIHelper) AddMessage(Content string, UserName string, IsUser bool, Save bool) {
	_ = a.AddMessageContext(context.Background(), Content, UserName, IsUser, Save)
}

func (a *AIHelper) AddMessageContext(ctx context.Context, Content string, UserName string, IsUser bool, Save bool) error {
	userMsg := model.Message{
		SessionID: a.SessionID,
		Content:   Content,
		UserName:  UserName,
		IsUser:    IsUser,
	}
	if Save {
		persisted, err := a.saveFunc(ctx, &userMsg)
		if err != nil {
			return err
		}
		userMsg = *persisted
	}
	a.mu.Lock()
	a.messages = append(a.messages, &userMsg)
	a.mu.Unlock()
	return nil
}

// SaveMessage 保存消息到数据库（通过回调函数避免循环依赖）
func (a *AIHelper) SetSaveFunc(saveFunc func(*model.Message) (*model.Message, error)) {
	a.saveFunc = func(_ context.Context, message *model.Message) (*model.Message, error) {
		return saveFunc(message)
	}
}

func (a *AIHelper) SetContextSaveFunc(saveFunc func(context.Context, *model.Message) (*model.Message, error)) {
	a.saveFunc = saveFunc
}

func (a *AIHelper) IsHistoryLoaded() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.historyLoaded
}

func (a *AIHelper) MarkHistoryLoaded() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.historyLoaded = true
}

func (a *AIHelper) SetModel(model AIModel) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = model
}

// GetMessages 获取所有消息历史
func (a *AIHelper) GetMessages() []*model.Message {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*model.Message, len(a.messages))
	copy(out, a.messages)
	return out
}

// buildContextMessages assembles bounded working and governed profile memory
// through the same deterministic Context Assembler used by the public view.
func (a *AIHelper) buildContextMessages(ctx context.Context) []*schema.Message {
	budget := GetContextTokenBudget()
	systemPrompt := config.GetConfig().MemoryConfig.SystemPrompt
	a.mu.RLock()
	messages := make([]*model.Message, len(a.messages))
	copy(messages, a.messages)
	userName, recallService, recallObserver := a.userName, a.profileRecall, a.profileObserver
	a.mu.RUnlock()
	working := make([]memorydomain.WorkingMessage, 0, len(messages))
	currentQuestion := ""
	for _, message := range messages {
		role := memorydomain.RoleAssistant
		if message.IsUser {
			role = memorydomain.RoleUser
			currentQuestion = message.Content
		}
		working = append(working, memorydomain.WorkingMessage{ID: message.ID, Role: role, Content: message.Content, CreatedAt: message.CreatedAt})
	}
	safetyRules := []string(nil)
	if systemPrompt != "" {
		safetyRules = append(safetyRules, systemPrompt)
	}
	profileFacts := make([]memorydomain.ProfileFact, 0, profiledomain.MaxRecallResults)
	status := "unavailable"
	startedAt := time.Now()
	if recallService != nil && userName != "" && currentQuestion != "" {
		if recalled, err := recallService.Recall(ctx, userName, userName, currentQuestion, profiledomain.MaxRecallResults); err == nil {
			status = recalled.Status
			for _, item := range recalled.Items {
				profileFacts = append(profileFacts, memorydomain.ProfileFact{Key: item.Key, Value: item.Value, Confidence: item.Confidence})
			}
		}
	}
	if recallObserver != nil {
		recallObserver.RecordProfileRecall(status, time.Since(startedAt), len(profileFacts))
	}
	if len(profileFacts) > 0 {
		safetyRules = append(safetyRules, "已确认环境记忆仅提供默认上下文；当前用户明确陈述或项目证据优先，冲突时不得沿用旧记忆。")
	}
	assembly := memorydomain.NewAssembler().Assemble(memorydomain.AssembleInput{
		SafetyRules: safetyRules, CurrentQuestion: currentQuestion, ProfileFacts: profileFacts, WorkingMessages: working, BudgetTokens: budget,
	})
	result := make([]*schema.Message, 0, len(assembly.Included))
	for _, item := range assembly.Included {
		role := schema.System
		switch item.Role {
		case memorydomain.RoleUser:
			role = schema.User
		case memorydomain.RoleAssistant:
			role = schema.Assistant
		}
		result = append(result, &schema.Message{Role: role, Content: item.Content})
	}
	return result
}

// GenerateResponse 同步生成
func (a *AIHelper) GenerateResponse(userName string, ctx context.Context, userQuestion string) (*model.Message, error) {
	if err := a.AddMessageContext(ctx, userQuestion, userName, true, true); err != nil {
		return nil, err
	}

	messages := a.buildContextMessages(ctx)
	a.mu.RLock()
	modelInstance := a.model
	a.mu.RUnlock()

	schemaMsg, err := modelInstance.GenerateResponse(ctx, messages)
	if err != nil {
		return nil, err
	}

	modelMsg := &model.Message{
		SessionID: a.SessionID,
		UserName:  userName,
		Content:   schemaMsg.Content,
		IsUser:    false,
	}

	if err := a.AddMessageContext(ctx, modelMsg.Content, userName, false, true); err != nil {
		return nil, err
	}
	return modelMsg, nil
}

// StreamResponse 流式生成
func (a *AIHelper) StreamResponse(userName string, ctx context.Context, cb StreamCallback, userQuestion string) (*model.Message, error) {
	if err := a.AddMessageContext(ctx, userQuestion, userName, true, true); err != nil {
		return nil, err
	}

	messages := a.buildContextMessages(ctx)
	a.mu.RLock()
	modelInstance := a.model
	a.mu.RUnlock()

	content, err := modelInstance.StreamResponse(ctx, messages, cb)
	if err != nil {
		return nil, err
	}

	modelMsg := &model.Message{
		SessionID: a.SessionID,
		UserName:  userName,
		Content:   content,
		IsUser:    false,
	}

	if err := a.AddMessageContext(ctx, modelMsg.Content, userName, false, true); err != nil {
		return nil, err
	}
	return modelMsg, nil
}

// GetModelType 获取模型类型
func (a *AIHelper) GetModelType() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model.GetModelType()
}
