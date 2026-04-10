package skill

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// MCPHelper 封装 MCP 客户端的连接、工具发现、工具调用
type MCPHelper struct {
	baseURL string
}

// NewMCPHelper 创建 MCP 帮助实例
func NewMCPHelper(baseURL string) *MCPHelper {
	return &MCPHelper{baseURL: baseURL}
}

// mcpSession 封装一次 MCP 连接的生命周期
type mcpSession struct {
	client *client.Client
}

func (h *MCPHelper) newSession(ctx context.Context) (*mcpSession, error) {
	httpTransport, err := transport.NewStreamableHTTP(h.baseURL)
	if err != nil {
		return nil, fmt.Errorf("create mcp transport failed: %w", err)
	}

	c := client.NewClient(httpTransport)

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "smartagent-client",
		Version: "1.0.0",
	}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}

	if _, err = c.Initialize(ctx, initReq); err != nil {
		return nil, fmt.Errorf("mcp initialize failed: %w", err)
	}

	return &mcpSession{client: c}, nil
}

func (s *mcpSession) Close() {
	if s.client != nil {
		s.client.Close()
	}
}

// ToolInfo 描述一个 MCP 工具，供 LLM 理解
type ToolInfo struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  map[string]string `json:"parameters"`
	Required    []string          `json:"required"`
}

// DiscoverTools 连接 MCP Server，列出所有可用工具并返回结构化描述
func (h *MCPHelper) DiscoverTools(ctx context.Context) ([]ToolInfo, error) {
	sess, err := h.newSession(ctx)
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	listResult, err := sess.client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list tools failed: %w", err)
	}

	var tools []ToolInfo
	for _, t := range listResult.Tools {
		info := ToolInfo{
			Name:        t.Name,
			Description: t.Description,
		}

		info.Parameters = make(map[string]string)

		// 直接遍历 t.InputSchema.Properties，不再进行类型断言
		for pName, pVal := range t.InputSchema.Properties {
			desc := ""
			// 这里的 pVal 依然是 interface{}，所以需要断言
			if pMap, ok2 := pVal.(map[string]interface{}); ok2 {
				if d, ok3 := pMap["description"].(string); ok3 {
					desc = d
				}
			}
			info.Parameters[pName] = desc
		}

		if t.InputSchema.Required != nil {
			info.Required = t.InputSchema.Required
		}

		tools = append(tools, info)
	}
	return tools, nil
}

// ToolCall 表示 LLM 决定要调用的一个工具
type ToolCall struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ToolCallResult 表示一次 MCP 工具调用的结果
type ToolCallResult struct {
	ToolName string `json:"tool_name"`
	Output   string `json:"output"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
}

// ExecuteToolCalls 批量执行 MCP 工具调用，每次调用复用同一个会话
func (h *MCPHelper) ExecuteToolCalls(ctx context.Context, calls []ToolCall) []ToolCallResult {
	results := make([]ToolCallResult, 0, len(calls))

	sess, err := h.newSession(ctx)
	if err != nil {
		for _, c := range calls {
			results = append(results, ToolCallResult{
				ToolName: c.Name,
				Success:  false,
				Error:    fmt.Sprintf("MCP 连接失败: %v", err),
			})
		}
		return results
	}
	defer sess.Close()

	for _, c := range calls {
		callReq := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      c.Name,
				Arguments: c.Arguments,
			},
		}

		result, callErr := sess.client.CallTool(ctx, callReq)
		if callErr != nil {
			results = append(results, ToolCallResult{
				ToolName: c.Name,
				Success:  false,
				Error:    callErr.Error(),
			})
			continue
		}

		var sb strings.Builder
		for _, content := range result.Content {
			if tc, ok := content.(mcp.TextContent); ok {
				sb.WriteString(tc.Text)
				sb.WriteString("\n")
			}
		}

		results = append(results, ToolCallResult{
			ToolName: c.Name,
			Output:   strings.TrimSpace(sb.String()),
			Success:  true,
		})
	}

	return results
}

// FormatToolsForLLM 将工具列表格式化为 LLM 可理解的文本描述
func FormatToolsForLLM(tools []ToolInfo) string {
	var sb strings.Builder
	for i, t := range tools {
		sb.WriteString(fmt.Sprintf("%d. %s - %s\n", i+1, t.Name, t.Description))
		if len(t.Parameters) > 0 {
			sb.WriteString("   参数:\n")
			for pName, pDesc := range t.Parameters {
				required := ""
				for _, r := range t.Required {
					if r == pName {
						required = " (必填)"
						break
					}
				}
				sb.WriteString(fmt.Sprintf("   - %s: %s%s\n", pName, pDesc, required))
			}
		}
	}
	return sb.String()
}
