package rcaagentplatform

import (
	"GopherAI/config"
	"GopherAI/internal/rcaagent"
	"context"
	"errors"
	modelOpenAI "github.com/cloudwego/eino-ext/components/model/openai"
	"os"
	"strings"
)

// Reuse the existing cloud model configuration, never accept credentials,
// provider URLs, models or arbitrary prompts from the browser.
func NewDefaultAgent(ctx context.Context) (*rcaagent.Agent, error) {
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		return nil, errors.New("RCA model is not configured")
	}
	cfg := config.GetConfig()
	// The autonomous investigation path intentionally uses the stronger,
	// low-frequency reasoning tier. A current environment override remains the
	// emergency/runtime control point; a stale qwen-turbo override is migrated.
	modelName := config.ResolveDeprecatedDashScopeModel(
		os.Getenv("GOPHERAI_RCA_MODEL"),
		cfg.RagBaseUrl,
		cfg.EffectiveReasoningModelName(),
	)
	temperature := float32(0)
	maxTokens := 1800
	m, err := modelOpenAI.NewChatModel(ctx, &modelOpenAI.ChatModelConfig{BaseURL: cfg.RagBaseUrl, APIKey: key, Model: modelName, Temperature: &temperature, MaxTokens: &maxTokens,
		ResponseFormat: &modelOpenAI.ChatCompletionResponseFormat{Type: modelOpenAI.ChatCompletionResponseFormatTypeJSONObject}})
	if err != nil {
		return nil, errors.New("RCA model initialization failed")
	}
	return rcaagent.New(m, modelName)
}
