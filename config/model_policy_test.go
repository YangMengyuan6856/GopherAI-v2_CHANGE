package config

import "testing"

func TestEffectiveDeepChatModelName(t *testing.T) {
	tests := []struct {
		name          string
		configuration RagModelConfig
		want          string
	}{
		{name: "enhanced tier", configuration: RagModelConfig{RagChatModelName: "flash", RagDeepChatModelName: "enhanced"}, want: "enhanced"},
		{name: "chat fallback", configuration: RagModelConfig{RagChatModelName: "flash"}, want: "flash"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.configuration.EffectiveDeepChatModelName(); got != test.want {
				t.Fatalf("EffectiveDeepChatModelName() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDeprecatedDashScopeModelGetsTieredFallbacks(t *testing.T) {
	configuration := RagModelConfig{
		RagBaseUrl:       "https://dashscope.aliyuncs.com/compatible-mode/v1",
		RagChatModelName: "qwen-turbo",
	}
	if got := configuration.EffectiveChatModelName(); got != "qwen3.7-flash-2026-07-15" {
		t.Fatalf("EffectiveChatModelName() = %q", got)
	}
	if got := configuration.EffectiveDeepChatModelName(); got != "qwen3.8-flash" {
		t.Fatalf("EffectiveDeepChatModelName() = %q", got)
	}
	if got := configuration.EffectiveReasoningModelName(); got != "qwen3.7-plus-2026-05-26" {
		t.Fatalf("EffectiveReasoningModelName() = %q", got)
	}
	if got := configuration.EffectiveJudgeModelName(); got != "qwen3.7-plus-2026-05-26" {
		t.Fatalf("EffectiveJudgeModelName() = %q", got)
	}
}

func TestNonDashScopeModelIsNotRewritten(t *testing.T) {
	configuration := RagModelConfig{RagBaseUrl: "https://example.com/v1", RagChatModelName: "qwen-turbo"}
	if got := configuration.EffectiveChatModelName(); got != "qwen-turbo" {
		t.Fatalf("EffectiveChatModelName() = %q, want original model", got)
	}
}

func TestResolveDeprecatedDashScopeModel(t *testing.T) {
	tests := []struct {
		name, model, baseURL, fallback, want string
	}{
		{name: "blank uses tier fallback", baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", fallback: "plus", want: "plus"},
		{name: "deprecated override uses tier fallback", model: "qwen-turbo", baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", fallback: "plus", want: "plus"},
		{name: "current override is preserved", model: "qwen3.8-flash", baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", fallback: "plus", want: "qwen3.8-flash"},
		{name: "other provider is untouched", model: "qwen-turbo", baseURL: "https://example.com/v1", fallback: "plus", want: "qwen-turbo"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ResolveDeprecatedDashScopeModel(test.model, test.baseURL, test.fallback); got != test.want {
				t.Fatalf("ResolveDeprecatedDashScopeModel() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestEffectiveReasoningModelName(t *testing.T) {
	tests := []struct {
		name          string
		configuration RagModelConfig
		want          string
	}{
		{name: "explicit reasoning tier", configuration: RagModelConfig{RagChatModelName: "flash", RagReasoningModelName: "plus"}, want: "plus"},
		{name: "legacy chat fallback", configuration: RagModelConfig{RagChatModelName: "flash"}, want: "flash"},
		{name: "trimmed value", configuration: RagModelConfig{RagReasoningModelName: "  plus  "}, want: "plus"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.configuration.EffectiveReasoningModelName(); got != test.want {
				t.Fatalf("EffectiveReasoningModelName() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestEffectiveJudgeModelName(t *testing.T) {
	tests := []struct {
		name          string
		configuration RagModelConfig
		want          string
	}{
		{name: "independent judge", configuration: RagModelConfig{RagChatModelName: "flash", RagReasoningModelName: "plus", RagJudgeModelName: "judge"}, want: "judge"},
		{name: "reasoning fallback", configuration: RagModelConfig{RagChatModelName: "flash", RagReasoningModelName: "plus"}, want: "plus"},
		{name: "chat fallback", configuration: RagModelConfig{RagChatModelName: "flash"}, want: "flash"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.configuration.EffectiveJudgeModelName(); got != test.want {
				t.Fatalf("EffectiveJudgeModelName() = %q, want %q", got, test.want)
			}
		})
	}
}
