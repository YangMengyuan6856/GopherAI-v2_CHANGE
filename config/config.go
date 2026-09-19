package config

import (
	"log"
	"strings"

	"github.com/BurntSushi/toml"
)

type MainConfig struct {
	Port               int    `toml:"port"`
	AppName            string `toml:"appName"`
	Host               string `toml:"host"`
	ContextTokenBudget int    `toml:"contextTokenBudget"` // 上下文 token 预算，0 或不设表示不限制
}

type EmailConfig struct {
	Authcode string `toml:"authcode"`
	Email    string `toml:"email" `
}

type RedisConfig struct {
	RedisPort     int    `toml:"port"`
	RedisDb       int    `toml:"db"`
	RedisHost     string `toml:"host"`
	RedisPassword string `toml:"password"`
}

type MysqlConfig struct {
	MysqlPort         int    `toml:"port"`
	MysqlHost         string `toml:"host"`
	MysqlUser         string `toml:"user"`
	MysqlPassword     string `toml:"password"`
	MysqlDatabaseName string `toml:"databaseName"`
	MysqlCharset      string `toml:"charset"`
}

type JwtConfig struct {
	ExpireDuration int    `toml:"expire_duration"`
	Issuer         string `toml:"issuer"`
	Subject        string `toml:"subject"`
	Key            string `toml:"key"`
}

type Rabbitmq struct {
	RabbitmqPort     int    `toml:"port"`
	RabbitmqHost     string `toml:"host"`
	RabbitmqUsername string `toml:"username"`
	RabbitmqPassword string `toml:"password"`
	RabbitmqVhost    string `toml:"vhost"`
}

type RagModelConfig struct {
	RagEmbeddingModel     string `toml:"embeddingModel"`
	RagChatModelName      string `toml:"chatModelName"`
	RagDeepChatModelName  string `toml:"deepChatModelName"`
	RagReasoningModelName string `toml:"reasoningModelName"`
	RagJudgeModelName     string `toml:"judgeModelName"`
	RagDocDir             string `toml:"docDir"`
	RagBaseUrl            string `toml:"baseUrl"`
	RagDimension          int    `toml:"dimension"`
}

const (
	deprecatedDashScopeChatModel = "qwen-turbo"
	defaultFastChatModel         = "qwen3.7-flash-2026-07-15"
	defaultDeepChatModel         = "qwen3.8-flash"
	defaultReasoningModel        = "qwen3.7-plus-2026-05-26"
)

// ResolveDeprecatedDashScopeModel preserves an explicit current model choice,
// but prevents a stale runtime override from bypassing the qwen-turbo
// retirement policy. An empty name resolves to the caller's tier fallback.
func ResolveDeprecatedDashScopeModel(name, baseURL, fallback string) string {
	name = strings.TrimSpace(name)
	fallback = strings.TrimSpace(fallback)
	if name == "" {
		return fallback
	}
	if strings.Contains(strings.ToLower(baseURL), "dashscope") && name == deprecatedDashScopeChatModel {
		return fallback
	}
	return name
}

// DeterministicGenerationExtraFields disables the default thinking mode of
// current DashScope Qwen3 models for classification, retrieval transforms and
// grounded answer generation. These bounded tasks need predictable JSON and
// latency; the separate reasoning and judge tiers intentionally keep their
// default reasoning behavior.
func (configuration RagModelConfig) DeterministicGenerationExtraFields(modelName string) map[string]any {
	if !configuration.usesDashScope() || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelName)), "qwen3.") {
		return nil
	}
	return map[string]any{"enable_thinking": false}
}

func (configuration RagModelConfig) usesDashScope() bool {
	return strings.Contains(strings.ToLower(configuration.RagBaseUrl), "dashscope")
}

// EffectiveChatModelName transparently keeps an older, server-preserved
// configuration away from qwen-turbo after its announced retirement. Explicit
// non-deprecated model choices are never rewritten.
func (configuration RagModelConfig) EffectiveChatModelName() string {
	name := strings.TrimSpace(configuration.RagChatModelName)
	if configuration.usesDashScope() && name == deprecatedDashScopeChatModel {
		return defaultFastChatModel
	}
	return name
}

// EffectiveDeepChatModelName allows the user-facing enhanced RAG paths to use
// a higher-quality model without making every intent or fast-RAG request pay
// that price.
func (configuration RagModelConfig) EffectiveDeepChatModelName() string {
	if name := strings.TrimSpace(configuration.RagDeepChatModelName); name != "" {
		return name
	}
	if configuration.usesDashScope() && strings.TrimSpace(configuration.RagChatModelName) == deprecatedDashScopeChatModel {
		return defaultDeepChatModel
	}
	return configuration.EffectiveChatModelName()
}

// EffectiveReasoningModelName keeps older deployments compatible while
// allowing costly reasoning to use a stronger model than high-volume RAG.
func (configuration RagModelConfig) EffectiveReasoningModelName() string {
	if name := strings.TrimSpace(configuration.RagReasoningModelName); name != "" {
		return name
	}
	if configuration.usesDashScope() && strings.TrimSpace(configuration.RagChatModelName) == deprecatedDashScopeChatModel {
		return defaultReasoningModel
	}
	return configuration.EffectiveChatModelName()
}

// EffectiveJudgeModelName deliberately supports an independent judge tier.
// If it is not configured, prefer the reasoning model and finally the chat
// model so existing configuration files remain valid.
func (configuration RagModelConfig) EffectiveJudgeModelName() string {
	if name := strings.TrimSpace(configuration.RagJudgeModelName); name != "" {
		return name
	}
	return configuration.EffectiveReasoningModelName()
}

type VoiceServiceConfig struct {
	VoiceServiceApiKey    string `toml:"voiceServiceApiKey"`
	VoiceServiceSecretKey string `toml:"voiceServiceSecretKey"`
}

type ReactConfig struct {
	MaxIterations int `toml:"maxIterations"`
}

type MemoryConfig struct {
	EnableSummary            bool    `toml:"enableSummary"`
	EnableLongTermMemory     bool    `toml:"enableLongTermMemory"`
	SummaryTriggerRatio      float64 `toml:"summaryTriggerRatio"`
	SummaryMaxTokens         int     `toml:"summaryMaxTokens"`
	LongTermMemoryMaxEntries int     `toml:"longTermMemoryMaxEntries"`
	SystemPrompt             string  `toml:"systemPrompt"`
}

type PprofConfig struct {
	Enabled             bool   `toml:"enabled"`
	Host                string `toml:"host"`
	Port                int    `toml:"port"`
	ReadTimeoutSeconds  int    `toml:"readTimeoutSeconds"`
	WriteTimeoutSeconds int    `toml:"writeTimeoutSeconds"`
}

type Config struct {
	EmailConfig        `toml:"emailConfig"`
	RedisConfig        `toml:"redisConfig"`
	MysqlConfig        `toml:"mysqlConfig"`
	JwtConfig          `toml:"jwtConfig"`
	MainConfig         `toml:"mainConfig"`
	Rabbitmq           `toml:"rabbitmqConfig"`
	RagModelConfig     `toml:"ragModelConfig"`
	VoiceServiceConfig `toml:"voiceServiceConfig"`
	ReactConfig        `toml:"reactConfig"`
	MemoryConfig       `toml:"memoryConfig"`
	PprofConfig        `toml:"pprofConfig"`
}

type RedisKeyConfig struct {
	CaptchaPrefix   string
	IndexName       string
	IndexNamePrefix string
}

var DefaultRedisKeyConfig = RedisKeyConfig{
	CaptchaPrefix:   "captcha:%s",
	IndexName:       "rag_docs:%s:idx",
	IndexNamePrefix: "rag_docs:%s:",
}

var config *Config

// InitConfig 初始化项目配置
func InitConfig() error {
	// 设置配置文件路径（相对于 main.go 所在的目录）
	if _, err := toml.DecodeFile("config/config.toml", config); err != nil {
		log.Fatal(err.Error())
		return err
	}
	return nil
}

func GetConfig() *Config {
	if config == nil {
		config = new(Config)
		_ = InitConfig()
	}
	return config
}
