package knowledge

import (
	"GopherAI/common/aihelper"
	"GopherAI/common/mysql"
	redisstore "GopherAI/common/redis"
	"GopherAI/config"
	knowledgeagent "GopherAI/internal/agent/knowledge"
	"GopherAI/internal/app"
	"GopherAI/internal/contract"
	"GopherAI/internal/policy"
	"GopherAI/internal/rag"
	"GopherAI/model"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	embeddingArk "github.com/cloudwego/eino-ext/components/embedding/ark"
	modelOpenAI "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Answerer interface {
	Answer(ctx context.Context, input knowledgeagent.Input) (knowledgeagent.Output, error)
}

type ConversationStore interface {
	ResolveSession(ctx context.Context, userID string, sessionID string, title string) (string, error)
	SaveExchange(ctx context.Context, userID string, sessionID string, question string, answer string) error
}

type ChatStrategy struct {
	answerer Answerer
	store    ConversationStore
}

func NewChatStrategy(answerer Answerer, store ConversationStore) (*ChatStrategy, error) {
	if answerer == nil || store == nil {
		return nil, errors.New("knowledge answerer and conversation store are required")
	}
	return &ChatStrategy{answerer: answerer, store: store}, nil
}

func NewDefaultChatStrategy() *ChatStrategy {
	strategy, err := NewChatStrategy(new(lazyDefaultAnswerer), &gormConversationStore{db: mysql.DB})
	if err != nil {
		panic(fmt.Sprintf("initialize rag_fast chat strategy: %v", err))
	}
	return strategy
}

func (*ChatStrategy) Name() string { return policy.RAGFastStrategyName }

func (strategy *ChatStrategy) Execute(ctx context.Context, request contract.RequestContext) (contract.AgentResult, error) {
	sessionID, err := strategy.store.ResolveSession(ctx, request.UserID, request.SessionID, request.Question)
	if err != nil {
		return contract.AgentResult{}, sessionError(err)
	}
	output, err := strategy.answerer.Answer(ctx, knowledgeagent.Input{
		TenantID: request.TenantID, UserID: request.UserID, Question: request.Question, TopK: rag.DefaultTopK,
	})
	if err != nil {
		return contract.AgentResult{SessionID: sessionID}, answerError(err)
	}
	output.Result.SessionID = sessionID
	if err := strategy.store.SaveExchange(ctx, request.UserID, sessionID, request.Question, output.Result.Answer); err != nil {
		return contract.AgentResult{SessionID: sessionID}, sessionError(err)
	}
	return output.Result, nil
}

func (strategy *ChatStrategy) Stream(ctx context.Context, request contract.RequestContext, emit app.StreamEmitter) (contract.AgentResult, error) {
	sessionID, err := strategy.store.ResolveSession(ctx, request.UserID, request.SessionID, request.Question)
	if err != nil {
		return contract.AgentResult{}, sessionError(err)
	}
	if err := emit(contract.StreamEvent{Type: contract.StreamEventMeta, SessionID: sessionID}); err != nil {
		return contract.AgentResult{SessionID: sessionID}, err
	}
	output, err := strategy.answerer.Answer(ctx, knowledgeagent.Input{
		TenantID: request.TenantID, UserID: request.UserID, Question: request.Question, TopK: rag.DefaultTopK,
	})
	if err != nil {
		return contract.AgentResult{SessionID: sessionID}, answerError(err)
	}
	output.Result.SessionID = sessionID
	if err := strategy.store.SaveExchange(ctx, request.UserID, sessionID, request.Question, output.Result.Answer); err != nil {
		return contract.AgentResult{SessionID: sessionID}, sessionError(err)
	}
	if err := emit(contract.StreamEvent{Type: contract.StreamEventDelta, SessionID: sessionID, Text: output.Result.Answer}); err != nil {
		return contract.AgentResult{SessionID: sessionID}, err
	}
	for index := range output.Result.Citations {
		citation := output.Result.Citations[index]
		if err := emit(contract.StreamEvent{Type: contract.StreamEventCitation, SessionID: sessionID, Citation: &citation}); err != nil {
			return contract.AgentResult{SessionID: sessionID}, err
		}
	}
	return output.Result, nil
}

func answerError(err error) *contract.DomainError {
	switch {
	case errors.Is(err, knowledgeagent.ErrInvalidQuestion), errors.Is(err, rag.ErrInvalidSearch):
		return contract.NewDomainError("INVALID_KNOWLEDGE_ANSWER", contract.ErrorValidation, "请输入有效的知识库问题", false, err)
	case errors.Is(err, knowledgeagent.ErrModelOutput), errors.Is(err, rag.ErrCitationVerification):
		return contract.NewDomainError("KNOWLEDGE_ANSWER_UNVERIFIED", contract.ErrorModel, "模型回答未通过引用校验，请稍后重试", true, err)
	default:
		return contract.NewDomainError("KNOWLEDGE_ANSWER_UNAVAILABLE", contract.ErrorDependencyUnavailable, "知识库回答暂时不可用", true, err)
	}
}

func sessionError(err error) *contract.DomainError {
	return contract.NewDomainError("KNOWLEDGE_SESSION_ERROR", contract.ErrorInternal, "知识库会话暂时不可用", true, err)
}

type gormConversationStore struct {
	db *gorm.DB
}

func (store *gormConversationStore) ResolveSession(ctx context.Context, userID string, sessionID string, title string) (string, error) {
	if store == nil || store.db == nil {
		return "", gorm.ErrInvalidDB
	}
	userID = strings.TrimSpace(userID)
	sessionID = strings.TrimSpace(sessionID)
	if userID == "" {
		return "", errors.New("user id is required")
	}
	if sessionID != "" {
		var count int64
		if err := store.db.WithContext(ctx).Model(&model.Session{}).Where("id = ? AND user_name = ?", sessionID, userID).Count(&count).Error; err != nil {
			return "", err
		}
		if count != 1 {
			return "", gorm.ErrRecordNotFound
		}
		return sessionID, nil
	}
	sessionID = uuid.NewString()
	return sessionID, store.db.WithContext(ctx).Create(&model.Session{ID: sessionID, UserName: userID, Title: boundedTitle(title)}).Error
}

func (store *gormConversationStore) SaveExchange(ctx context.Context, userID string, sessionID string, question string, answer string) error {
	if store == nil || store.db == nil {
		return gorm.ErrInvalidDB
	}
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		messages := []model.Message{
			{SessionID: sessionID, UserName: userID, Content: question, IsUser: true},
			{SessionID: sessionID, UserName: userID, Content: answer, IsUser: false},
		}
		if err := tx.Create(&messages).Error; err != nil {
			return err
		}
		return tx.Model(&model.Session{}).Where("id = ? AND user_name = ?", sessionID, userID).Update("updated_at", time.Now().UTC()).Error
	})
	if err != nil {
		return err
	}
	if helper, exists := aihelper.GetGlobalManager().GetAIHelper(userID, sessionID); exists {
		helper.AddMessage(question, userID, true, false)
		helper.AddMessage(answer, userID, false, false)
	}
	return nil
}

func boundedTitle(title string) string {
	runes := []rune(strings.TrimSpace(title))
	if len(runes) > 100 {
		runes = runes[:100]
	}
	return string(runes)
}

type lazyDefaultAnswerer struct {
	once     sync.Once
	answerer Answerer
	err      error
}

func (answerer *lazyDefaultAnswerer) Answer(ctx context.Context, input knowledgeagent.Input) (knowledgeagent.Output, error) {
	answerer.once.Do(func() {
		answerer.answerer, answerer.err = newDefaultAnswerer()
	})
	if answerer.err != nil {
		return knowledgeagent.Output{}, answerer.err
	}
	return answerer.answerer.Answer(ctx, input)
}

func newDefaultAnswerer() (Answerer, error) {
	configuration := config.GetConfig()
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return nil, errors.New("knowledge model API key is not configured")
	}
	timeout := 45 * time.Second
	retryTimes := 1
	embedder, err := embeddingArk.NewEmbedder(context.Background(), &embeddingArk.EmbeddingConfig{
		BaseURL: configuration.RagBaseUrl, APIKey: apiKey, Model: configuration.RagEmbeddingModel,
		Timeout: &timeout, RetryTimes: &retryTimes,
	})
	if err != nil {
		return nil, err
	}
	retriever, err := rag.NewHybridRetriever(
		rag.NewRedisSearchBackend(redisstore.Rdb), embedder,
		rag.NewGormAuthorityRepository(mysql.DB), strings.TrimSpace(os.Getenv("GOPHERAI_ENV")), configuration.RagDimension,
	)
	if err != nil {
		return nil, err
	}
	chatModel, err := modelOpenAI.NewChatModel(context.Background(), &modelOpenAI.ChatModelConfig{
		BaseURL: configuration.RagBaseUrl, APIKey: apiKey, Model: configuration.RagChatModelName,
	})
	if err != nil {
		return nil, err
	}
	return knowledgeagent.NewAgent(retriever, chatModel, rag.DefaultEvidenceGate(), rag.NewCitationBuilder())
}

var _ app.ChatStrategy = (*ChatStrategy)(nil)
