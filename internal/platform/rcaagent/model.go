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
	modelName := strings.TrimSpace(os.Getenv("GOPHERAI_RCA_MODEL"))
	if modelName == "" {
		modelName = cfg.RagChatModelName
		// This isolated multi-step task needs stronger instruction following
		// than the legacy lightweight chat default. Other routes are unchanged.
		if modelName == "qwen-turbo" && strings.Contains(cfg.RagBaseUrl, "dashscope") {
			modelName = "qwen-plus"
		}
	}
	temperature := float32(0)
	maxTokens := 1800
	m, err := modelOpenAI.NewChatModel(ctx, &modelOpenAI.ChatModelConfig{BaseURL: cfg.RagBaseUrl, APIKey: key, Model: modelName, Temperature: &temperature, MaxTokens: &maxTokens,
		ResponseFormat: &modelOpenAI.ChatCompletionResponseFormat{Type: modelOpenAI.ChatCompletionResponseFormatTypeJSONObject}})
	if err != nil {
		return nil, errors.New("RCA model initialization failed")
	}
	return rcaagent.New(m, modelName)
}
