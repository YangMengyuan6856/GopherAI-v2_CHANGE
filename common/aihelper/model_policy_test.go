package aihelper

import (
	"testing"

	"GopherAI/config"
)

func TestResolveOpenAISettingsMigratesDeprecatedDashScopeChat(t *testing.T) {
	policy := config.RagModelConfig{
		RagBaseUrl:           "https://dashscope.aliyuncs.com/compatible-mode/v1",
		RagChatModelName:     "qwen-turbo",
		RagDeepChatModelName: "qwen3.8-flash",
	}
	modelName, baseURL := resolveOpenAISettings("qwen-turbo", policy.RagBaseUrl, policy)
	if modelName != "qwen3.8-flash" {
		t.Fatalf("modelName = %q, want qwen3.8-flash", modelName)
	}
	if baseURL != policy.RagBaseUrl {
		t.Fatalf("baseURL = %q, want %q", baseURL, policy.RagBaseUrl)
	}
}

func TestResolveOpenAISettingsPreservesExplicitModel(t *testing.T) {
	policy := config.RagModelConfig{
		RagBaseUrl:           "https://dashscope.aliyuncs.com/compatible-mode/v1",
		RagChatModelName:     "qwen3.7-flash-2026-07-15",
		RagDeepChatModelName: "qwen3.8-flash",
	}
	modelName, _ := resolveOpenAISettings("qwen3.7-plus-2026-05-26", policy.RagBaseUrl, policy)
	if modelName != "qwen3.7-plus-2026-05-26" {
		t.Fatalf("modelName = %q, want explicit environment model", modelName)
	}
}

func TestResolveOpenAISettingsFallsBackToConfiguredEnhancedTier(t *testing.T) {
	policy := config.RagModelConfig{
		RagBaseUrl:           "https://dashscope.aliyuncs.com/compatible-mode/v1",
		RagChatModelName:     "qwen3.7-flash-2026-07-15",
		RagDeepChatModelName: "qwen3.8-flash",
	}
	modelName, baseURL := resolveOpenAISettings("", "", policy)
	if modelName != "qwen3.8-flash" || baseURL != policy.RagBaseUrl {
		t.Fatalf("resolved (%q, %q), want (%q, %q)", modelName, baseURL, "qwen3.8-flash", policy.RagBaseUrl)
	}
}
