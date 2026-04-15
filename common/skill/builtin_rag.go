package skill

import (
	"GopherAI/common/rag"
	"context"
	"fmt"
	"log"
	"strings"
)

const RAGQuerySkillCode = "rag_query"

// RAGQuerySkill 知识库检索技能，复用项目已有的 RAG 能力
// 根据用户问题在其上传的文档中检索相关内容
type RAGQuerySkill struct{}

func NewRAGQuerySkill() *RAGQuerySkill { return &RAGQuerySkill{} }

func (r *RAGQuerySkill) Code() string { return RAGQuerySkillCode }
func (r *RAGQuerySkill) Name() string { return "知识库检索" }
func (r *RAGQuerySkill) Description() string {
	return "在用户上传的文档知识库中检索相关信息，示例：/skill rag_query 什么是向量数据库"
}

func (r *RAGQuerySkill) Execute(ctx context.Context, req *ExecuteRequest) (*ExecuteResult, error) {
	query := req.Args["query"]
	if query == "" {
		return &ExecuteResult{
			SkillCode: RAGQuerySkillCode,
			Output:    "请提供要检索的问题，示例：/skill rag_query 你的问题",
		}, nil
	}

	userName := req.UserName
	if userName == "" {
		return &ExecuteResult{
			SkillCode: RAGQuerySkillCode,
			Output:    "无法确定用户身份，无法检索知识库",
		}, nil
	}

	ragQuery, err := rag.NewRAGQuery(ctx, userName)
	if err != nil {
		log.Printf("[RAGQuerySkill] create rag query failed for user %s: %v", userName, err)
		return &ExecuteResult{
			SkillCode: RAGQuerySkillCode,
			Output:    fmt.Sprintf("知识库不可用（用户 %s 可能未上传文档）。请尝试使用 web_search 工具从互联网获取信息。", userName),
		}, nil
	}

	docs, err := ragQuery.RetrieveDocuments(ctx, query)
	if err != nil {
		log.Printf("[RAGQuerySkill] retrieve documents failed: %v", err)
		return &ExecuteResult{
			SkillCode: RAGQuerySkillCode,
			Output:    "知识库检索失败，请稍后重试",
		}, nil
	}

	if len(docs) == 0 {
		return &ExecuteResult{
			SkillCode: RAGQuerySkillCode,
			Output:    "未在知识库中找到相关内容",
		}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("从知识库中检索到 %d 条相关内容：\n\n", len(docs)))
	for i, doc := range docs {
		content := doc.Content
		// 截断过长的单条结果
		if len([]rune(content)) > 500 {
			content = string([]rune(content)[:500]) + "..."
		}
		sb.WriteString(fmt.Sprintf("[文档 %d] %s\n\n", i+1, content))
	}

	return &ExecuteResult{
		SkillCode: RAGQuerySkillCode,
		Output:    sb.String(),
		Data: map[string]interface{}{
			"query":     query,
			"doc_count": len(docs),
		},
	}, nil
}
