package skill

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
)

const TranslateSkillCode = "translate"

// TranslateSkill 翻译助手技能，自动检测中英文并互译，复用阿里百炼 API
type TranslateSkill struct{}

func NewTranslateSkill() *TranslateSkill { return &TranslateSkill{} }

func (t *TranslateSkill) Code() string        { return TranslateSkillCode }
func (t *TranslateSkill) Name() string        { return "翻译助手" }
func (t *TranslateSkill) Description() string { return "中英文互译，示例：/skill translate Hello World" }

func (t *TranslateSkill) Execute(ctx context.Context, req *ExecuteRequest) (*ExecuteResult, error) {
	text := req.Args["query"]
	if text == "" {
		return &ExecuteResult{
			SkillCode: TranslateSkillCode,
			Output:    "请提供要翻译的文本，示例：/skill translate Hello World",
		}, nil
	}

	direction := detectLanguageDirection(text)

	prompt := buildTranslatePrompt(text, direction)

	result, err := callLLMForSkill(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("翻译失败: %w", err)
	}

	return &ExecuteResult{
		SkillCode: TranslateSkillCode,
		Output:    fmt.Sprintf("【%s】\n%s", direction, result),
		Data: map[string]interface{}{
			"source":    text,
			"direction": direction,
			"result":    result,
		},
	}, nil
}

func detectLanguageDirection(text string) string {
	chineseCount := 0
	totalCount := 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			totalCount++
			if unicode.Is(unicode.Han, r) {
				chineseCount++
			}
		}
	}
	if totalCount == 0 {
		return "中文 → 英文"
	}
	if float64(chineseCount)/float64(totalCount) > 0.3 {
		return "中文 → 英文"
	}
	return "英文 → 中文"
}

func buildTranslatePrompt(text, direction string) string {
	if strings.Contains(direction, "中文 → 英文") {
		return fmt.Sprintf("请将以下中文翻译成英文，只返回翻译结果，不要添加任何解释：\n\n%s", text)
	}
	return fmt.Sprintf("请将以下英文翻译成中文，只返回翻译结果，不要添加任何解释：\n\n%s", text)
}

// callLLMForSkill 技能内部调用 LLM（复用项目已有的阿里百炼配置）
func callLLMForSkill(ctx context.Context, prompt string) (string, error) {
	key := os.Getenv("OPENAI_API_KEY")
	baseURL := os.Getenv("OPENAI_BASE_URL")
	modelName := os.Getenv("OPENAI_MODEL_NAME")

	llm, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: baseURL,
		Model:   modelName,
		APIKey:  key,
	})
	if err != nil {
		return "", fmt.Errorf("create llm failed: %w", err)
	}

	messages := []*schema.Message{
		{Role: schema.User, Content: prompt},
	}

	resp, err := llm.Generate(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("llm generate failed: %w", err)
	}
	return strings.TrimSpace(resp.Content), nil
}
