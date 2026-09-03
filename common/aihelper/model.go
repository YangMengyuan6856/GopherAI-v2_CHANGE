package aihelper

import (
	"GopherAI/common/rag"
	"GopherAI/config"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/ollama"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type StreamCallback func(msg string)

// AIModel 定义AI模型接口
type AIModel interface {
	GenerateResponse(ctx context.Context, messages []*schema.Message) (*schema.Message, error)
	StreamResponse(ctx context.Context, messages []*schema.Message, cb StreamCallback) (string, error)
	GetModelType() string
}

// =================== OpenAI 实现 ===================
type OpenAIModel struct {
	llm model.ToolCallingChatModel
}

func NewOpenAIModel(ctx context.Context) (*OpenAIModel, error) {
	key := os.Getenv("OPENAI_API_KEY")
	modelName := os.Getenv("OPENAI_MODEL_NAME")
	baseURL := os.Getenv("OPENAI_BASE_URL")

	llm, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: baseURL,
		Model:   modelName,
		APIKey:  key,
	})
	if err != nil {
		return nil, fmt.Errorf("create openai model failed: %v", err)
	}
	return &OpenAIModel{llm: llm}, nil
}

func (o *OpenAIModel) GenerateResponse(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	resp, err := o.llm.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("openai generate failed: %v", err)
	}
	return resp, nil
}

func (o *OpenAIModel) StreamResponse(ctx context.Context, messages []*schema.Message, cb StreamCallback) (string, error) {
	stream, err := o.llm.Stream(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("openai stream failed: %v", err)
	}
	defer stream.Close()

	var fullResp strings.Builder

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("openai stream recv failed: %v", err)
		}
		if len(msg.Content) > 0 {
			fullResp.WriteString(msg.Content) // 聚合

			cb(msg.Content) // 实时调用cb函数，方便主动发送给前端
		}
	}

	return fullResp.String(), nil //返回完整内容，方便后续存储
}

func (o *OpenAIModel) GetModelType() string { return "1" }

// GenerateForSummary 实现 SummaryLLM 接口，用于摘要生成和记忆提取
func (o *OpenAIModel) GenerateForSummary(ctx context.Context, messages []*schema.Message) (string, error) {
	resp, err := o.llm.Generate(ctx, messages)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// =================== Ollama 实现 ===================

// OllamaModel Ollama模型实现
type OllamaModel struct {
	llm model.ToolCallingChatModel
}

func NewOllamaModel(ctx context.Context, baseURL, modelName string) (*OllamaModel, error) {
	llm, err := ollama.NewChatModel(ctx, &ollama.ChatModelConfig{
		BaseURL: baseURL,
		Model:   modelName,
	})
	if err != nil {
		return nil, fmt.Errorf("create ollama model failed: %v", err)
	}
	return &OllamaModel{llm: llm}, nil
}

func (o *OllamaModel) GenerateResponse(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	resp, err := o.llm.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("ollama generate failed: %v", err)
	}
	return resp, nil
}

func (o *OllamaModel) StreamResponse(ctx context.Context, messages []*schema.Message, cb StreamCallback) (string, error) {
	stream, err := o.llm.Stream(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("ollama stream failed: %v", err)
	}
	defer stream.Close()
	var fullResp strings.Builder
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("openai stream recv failed: %v", err)
		}
		if len(msg.Content) > 0 {
			fullResp.WriteString(msg.Content) // 聚合
			cb(msg.Content)                   // 实时调用cb函数，方便主动发送给前端
		}
	}
	return fullResp.String(), nil //返回完整内容，方便后续存储
}

func (o *OllamaModel) GetModelType() string { return "4" }

func (o *OllamaModel) GenerateForSummary(ctx context.Context, messages []*schema.Message) (string, error) {
	resp, err := o.llm.Generate(ctx, messages)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// =================== RAG 实现 ===================
type AliRAGModel struct {
	llm      model.ToolCallingChatModel
	username string // 用于获取用户的文档
}

func NewAliRAGModel(ctx context.Context, username string) (*AliRAGModel, error) {
	key := os.Getenv("OPENAI_API_KEY")
	conf := config.GetConfig()
	modelName := conf.RagModelConfig.RagChatModelName
	baseURL := conf.RagModelConfig.RagBaseUrl

	llm, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: baseURL,
		Model:   modelName,
		APIKey:  key,
	})
	if err != nil {
		return nil, fmt.Errorf("create ali rag model failed: %v", err)
	}
	return &AliRAGModel{
		llm:      llm,
		username: username,
	}, nil
}

func (o *AliRAGModel) GenerateResponse(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	// 1. 创建 RAG 查询器
	ragQuery, err := rag.NewRAGQuery(ctx, o.username)
	if err != nil {
		log.Printf("Failed to create RAG query (user may not have uploaded file): %v", err)
		// 如果用户没有上传文件，直接使用原始问题
		resp, err := o.llm.Generate(ctx, messages)
		if err != nil {
			return nil, fmt.Errorf("ali rag generate failed: %v", err)
		}
		return resp, nil
	}

	// 2. 获取用户最后一条消息作为查询
	if len(messages) == 0 {
		return nil, fmt.Errorf("no messages provided")
	}
	lastMessage := messages[len(messages)-1]
	query := lastMessage.Content

	// 3. 检索相关文档
	docs, err := ragQuery.RetrieveDocuments(ctx, query)
	if err != nil {
		log.Printf("Failed to retrieve documents: %v", err)
		// 检索失败，使用原始问题
		resp, err := o.llm.Generate(ctx, messages)
		if err != nil {
			return nil, fmt.Errorf("ali rag generate failed: %v", err)
		}
		return resp, nil
	}

	// 4. 构建包含检索结果的提示词
	ragPrompt := rag.BuildRAGPrompt(query, docs)

	// 5. 替换最后一条消息为 RAG 提示词
	ragMessages := make([]*schema.Message, len(messages))
	copy(ragMessages, messages)
	ragMessages[len(ragMessages)-1] = &schema.Message{
		Role:    schema.User,
		Content: ragPrompt,
	}

	// 6. 调用 LLM 生成回答
	resp, err := o.llm.Generate(ctx, ragMessages)
	if err != nil {
		return nil, fmt.Errorf("ali rag generate failed: %v", err)
	}
	return resp, nil
}

func (o *AliRAGModel) StreamResponse(ctx context.Context, messages []*schema.Message, cb StreamCallback) (string, error) {
	// 1. 创建 RAG 查询器
	ragQuery, err := rag.NewRAGQuery(ctx, o.username)
	if err != nil {
		log.Printf("Failed to create RAG query (user may not have uploaded file): %v", err)
		// 如果用户没有上传文件，直接使用原始问题
		return o.streamWithoutRAG(ctx, messages, cb)
	}

	// 2. 获取用户最后一条消息作为查询
	if len(messages) == 0 {
		return "", fmt.Errorf("no messages provided")
	}
	lastMessage := messages[len(messages)-1]
	query := lastMessage.Content

	// 3. 检索相关文档
	docs, err := ragQuery.RetrieveDocuments(ctx, query)
	if err != nil {
		log.Printf("Failed to retrieve documents: %v", err)
		// 检索失败，使用原始问题
		return o.streamWithoutRAG(ctx, messages, cb)
	}

	// 4. 构建包含检索结果的提示词
	ragPrompt := rag.BuildRAGPrompt(query, docs)

	// 5. 替换最后一条消息为 RAG 提示词
	ragMessages := make([]*schema.Message, len(messages))
	copy(ragMessages, messages)
	ragMessages[len(ragMessages)-1] = &schema.Message{
		Role:    schema.User,
		Content: ragPrompt,
	}

	// 6. 流式调用 LLM
	stream, err := o.llm.Stream(ctx, ragMessages)
	if err != nil {
		return "", fmt.Errorf("ali rag stream failed: %v", err)
	}
	defer stream.Close()

	var fullResp strings.Builder

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("ali rag stream recv failed: %v", err)
		}
		if len(msg.Content) > 0 {
			fullResp.WriteString(msg.Content)
			cb(msg.Content)
		}
	}

	return fullResp.String(), nil
}

// streamWithoutRAG 当没有 RAG 文档时的流式响应
func (o *AliRAGModel) streamWithoutRAG(ctx context.Context, messages []*schema.Message, cb StreamCallback) (string, error) {
	stream, err := o.llm.Stream(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("ali rag stream failed: %v", err)
	}
	defer stream.Close()

	var fullResp strings.Builder

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("ali rag stream recv failed: %v", err)
		}
		if len(msg.Content) > 0 {
			fullResp.WriteString(msg.Content)
			cb(msg.Content)
		}
	}

	return fullResp.String(), nil
}

func (o *AliRAGModel) GetModelType() string { return "2" }

func (o *AliRAGModel) GenerateForSummary(ctx context.Context, messages []*schema.Message) (string, error) {
	resp, err := o.llm.Generate(ctx, messages)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}
