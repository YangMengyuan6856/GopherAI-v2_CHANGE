package intentplatform

import (
	"GopherAI/common/mysql"
	"GopherAI/config"
	"GopherAI/internal/contract"
	intentdomain "GopherAI/internal/intent"
	"GopherAI/model"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	embeddingArk "github.com/cloudwego/eino-ext/components/embedding/ark"
	modelOpenAI "github.com/cloudwego/eino-ext/components/model/openai"
	"gorm.io/gorm"
)

const runtimeFailureBackoff = 30 * time.Second

type PreviousIntentResolver interface {
	Resolve(ctx context.Context, userID string, sessionID string) (string, error)
}

type runtimeCascade interface {
	Recognize(ctx context.Context, input intentdomain.CascadeInput) intentdomain.CascadeDecision
}

type cascadeFactory func() (runtimeCascade, error)

type RuntimeRecognizer struct {
	resolver PreviousIntentResolver
	factory  cascadeFactory
	backoff  time.Duration

	mu          sync.Mutex
	cascade     runtimeCascade
	lastFailure time.Time
	lastError   error
}

func NewDefaultRecognizer() *RuntimeRecognizer {
	return newRuntimeRecognizer(&gormPreviousIntentResolver{db: mysql.DB}, newDefaultCascade, runtimeFailureBackoff)
}

func newRuntimeRecognizer(resolver PreviousIntentResolver, factory cascadeFactory, backoff time.Duration) *RuntimeRecognizer {
	return &RuntimeRecognizer{resolver: resolver, factory: factory, backoff: backoff}
}

func (recognizer *RuntimeRecognizer) Recognize(ctx context.Context, input intentdomain.CascadeInput) intentdomain.CascadeDecision {
	contextFallback := ""
	if strings.TrimSpace(input.PreviousIntent) == "" && strings.TrimSpace(input.SessionID) != "" && recognizer.resolver != nil {
		previous, err := recognizer.resolver.Resolve(ctx, input.UserID, input.SessionID)
		if err == nil {
			input.PreviousIntent = previous
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			contextFallback = "previous_intent_unavailable"
		}
	}
	cascade, err := recognizer.resolveCascade()
	if err != nil {
		return unavailableDecision(contextFallback)
	}
	decision := cascade.Recognize(ctx, input)
	if contextFallback != "" {
		decision.Diagnostics.FallbackReasons = append(decision.Diagnostics.FallbackReasons, contextFallback)
	}
	return decision
}

func (recognizer *RuntimeRecognizer) resolveCascade() (runtimeCascade, error) {
	recognizer.mu.Lock()
	defer recognizer.mu.Unlock()
	if recognizer.cascade != nil {
		return recognizer.cascade, nil
	}
	if recognizer.lastError != nil && time.Since(recognizer.lastFailure) < recognizer.backoff {
		return nil, recognizer.lastError
	}
	if recognizer.factory == nil {
		recognizer.lastError = errors.New("intent cascade factory is missing")
		recognizer.lastFailure = time.Now()
		return nil, recognizer.lastError
	}
	cascade, err := recognizer.factory()
	if err != nil {
		recognizer.lastError, recognizer.lastFailure = err, time.Now()
		return nil, err
	}
	recognizer.cascade, recognizer.lastError = cascade, nil
	return recognizer.cascade, nil
}

func newDefaultCascade() (runtimeCascade, error) {
	configuration := config.GetConfig()
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return nil, errors.New("intent model API key is not configured")
	}
	embeddingTimeout := 2500 * time.Millisecond
	retryTimes := 1
	embedder, err := embeddingArk.NewEmbedder(context.Background(), &embeddingArk.EmbeddingConfig{
		BaseURL: configuration.RagBaseUrl, APIKey: apiKey, Model: configuration.RagEmbeddingModel,
		Timeout: &embeddingTimeout, RetryTimes: &retryTimes,
	})
	if err != nil {
		return nil, err
	}
	chatModel, err := modelOpenAI.NewChatModel(context.Background(), &modelOpenAI.ChatModelConfig{
		BaseURL: configuration.RagBaseUrl, APIKey: apiKey, Model: configuration.RagChatModelName,
		ResponseFormat: &modelOpenAI.ChatCompletionResponseFormat{
			Type: modelOpenAI.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return nil, err
	}
	prototype, err := intentdomain.NewPrototypeRecognizer(embedder, intentdomain.DefaultPrototypeConfig())
	if err != nil {
		return nil, err
	}
	llm, err := intentdomain.NewStructuredLLMRecognizer(chatModel, 4*time.Second)
	if err != nil {
		return nil, err
	}
	return intentdomain.NewCascadeRecognizer(intentdomain.NewPatternRecognizer(), prototype, llm)
}

type gormPreviousIntentResolver struct {
	db *gorm.DB
}

func (resolver *gormPreviousIntentResolver) Resolve(ctx context.Context, userID string, sessionID string) (string, error) {
	if resolver == nil || resolver.db == nil {
		return "", gorm.ErrInvalidDB
	}
	var run model.AgentRun
	err := resolver.db.WithContext(ctx).
		Select("shadow_intent").
		Where("session_id = ? AND user_id_hash = ? AND shadow_intent <> ''", strings.TrimSpace(sessionID), hashUserID(userID)).
		Order("id DESC").
		Take(&run).Error
	if err != nil {
		return "", err
	}
	if !intentdomain.IsKnown(run.ShadowIntent) {
		return "", gorm.ErrRecordNotFound
	}
	return run.ShadowIntent, nil
}

func unavailableDecision(contextFallback string) intentdomain.CascadeDecision {
	fallbacks := []string{"intent_runtime_unavailable"}
	if contextFallback != "" {
		fallbacks = append(fallbacks, contextFallback)
	}
	return intentdomain.CascadeDecision{
		Result: intentdomainUnavailableResult(),
		Diagnostics: intentdomain.CascadeDiagnostics{
			Version: intentdomain.CascadeVersion, FinalStage: "unavailable", FallbackReasons: fallbacks,
		},
	}
}

func intentdomainUnavailableResult() contract.IntentResult {
	return contract.IntentResult{
		Intent: intentdomain.General, Confidence: 0, NeedsClarify: true, Version: intentdomain.CascadeVersion,
		Stages: []contract.IntentStageResult{{Stage: "cascade", Intent: intentdomain.General, Confidence: 0, ReasonCode: "intent_runtime_unavailable"}},
	}
}

func hashUserID(userID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(userID)))
	return hex.EncodeToString(sum[:])
}
