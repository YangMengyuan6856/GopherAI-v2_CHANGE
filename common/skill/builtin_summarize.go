package skill

import (
	"context"
	"fmt"
	"strings"
)

const SummarizeSkillCode = "summarize"

// SummarizeSkill 文本摘要技能，调用 LLM 对长文本生成精炼摘要
type SummarizeSkill struct{}

func NewSummarizeSkill() *SummarizeSkill { return &SummarizeSkill{} }

func (s *SummarizeSkill) Code() string        { return SummarizeSkillCode }
func (s *SummarizeSkill) Name() string        { return "文本摘要" }
func (s *SummarizeSkill) Description() string { return "对长文本生成摘要，示例：/skill summarize 你的长文本内容" }

func (s *SummarizeSkill) Execute(ctx context.Context, req *ExecuteRequest) (*ExecuteResult, error) {
	text := req.Args["query"]
	if text == "" {
		return &ExecuteResult{
			SkillCode: SummarizeSkillCode,
			Output:    "请提供要摘要的文本内容，示例：/skill summarize 你的长文本内容",
		}, nil
	}

	prompt := fmt.Sprintf(
		"请对以下文本生成简洁的中文摘要，保留核心要点，以条目形式列出：\n\n%s",
		text,
	)

	result, err := callLLMForSkill(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("摘要生成失败: %w", err)
	}

	return &ExecuteResult{
		SkillCode: SummarizeSkillCode,
		Output:    fmt.Sprintf("【文本摘要】\n%s", strings.TrimSpace(result)),
		Data: map[string]interface{}{
			"original_length": len([]rune(text)),
			"summary":         result,
		},
	}, nil
}
