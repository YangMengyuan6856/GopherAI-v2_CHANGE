// Package toolruntime provides the protocol-facing building blocks used by
// governed tool execution. Policy, approval, timeout and audit capabilities
// can be added here without coupling them to chat commands or UI concepts.
package toolruntime

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// ToolInfo is the model-facing description of an executable tool.
type ToolInfo struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  map[string]string `json:"parameters"`
	Required    []string          `json:"required"`
}

// ToolCall is one tool invocation requested by the model.
type ToolCall struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ToolCallResult is the normalized result of one tool invocation.
type ToolCallResult struct {
	ToolName string `json:"tool_name"`
	Output   string `json:"output"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
}

// MCPClient owns MCP connection, discovery and execution details.
type MCPClient struct {
	baseURL string
}

// NewMCPClient creates an MCP protocol client for a server endpoint.
func NewMCPClient(baseURL string) *MCPClient {
	return &MCPClient{baseURL: baseURL}
}

type mcpSession struct {
	client *client.Client
}

func (c *MCPClient) newSession(ctx context.Context) (*mcpSession, error) {
	httpTransport, err := transport.NewStreamableHTTP(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("create mcp transport failed: %w", err)
	}

	mcpClient := client.NewClient(httpTransport)
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "gopherai-tool-runtime",
		Version: "1.0.0",
	}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}

	if _, err = mcpClient.Initialize(ctx, initReq); err != nil {
		mcpClient.Close()
		return nil, fmt.Errorf("mcp initialize failed: %w", err)
	}

	return &mcpSession{client: mcpClient}, nil
}

func (s *mcpSession) Close() {
	if s.client != nil {
		s.client.Close()
	}
}

// DiscoverTools lists the tools currently exposed by the MCP server.
func (c *MCPClient) DiscoverTools(ctx context.Context) ([]ToolInfo, error) {
	sess, err := c.newSession(ctx)
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	listResult, err := sess.client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list tools failed: %w", err)
	}

	tools := make([]ToolInfo, 0, len(listResult.Tools))
	for _, tool := range listResult.Tools {
		info := ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  make(map[string]string),
		}

		for parameterName, parameterValue := range tool.InputSchema.Properties {
			description := ""
			if parameterMap, ok := parameterValue.(map[string]interface{}); ok {
				if value, ok := parameterMap["description"].(string); ok {
					description = value
				}
			}
			info.Parameters[parameterName] = description
		}
		if tool.InputSchema.Required != nil {
			info.Required = tool.InputSchema.Required
		}
		tools = append(tools, info)
	}

	return tools, nil
}

// ExecuteToolCalls executes a batch of calls over one MCP session.
func (c *MCPClient) ExecuteToolCalls(ctx context.Context, calls []ToolCall) []ToolCallResult {
	results := make([]ToolCallResult, 0, len(calls))
	sess, err := c.newSession(ctx)
	if err != nil {
		for _, call := range calls {
			results = append(results, ToolCallResult{
				ToolName: call.Name,
				Success:  false,
				Error:    fmt.Sprintf("MCP 连接失败: %v", err),
			})
		}
		return results
	}
	defer sess.Close()

	for _, call := range calls {
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      call.Name,
				Arguments: call.Arguments,
			},
		}

		result, callErr := sess.client.CallTool(ctx, request)
		if callErr != nil {
			results = append(results, ToolCallResult{
				ToolName: call.Name,
				Success:  false,
				Error:    callErr.Error(),
			})
			continue
		}

		var output strings.Builder
		for _, content := range result.Content {
			if textContent, ok := content.(mcp.TextContent); ok {
				output.WriteString(textContent.Text)
				output.WriteString("\n")
			}
		}

		results = append(results, ToolCallResult{
			ToolName: call.Name,
			Output:   strings.TrimSpace(output.String()),
			Success:  true,
		})
	}

	return results
}

// FormatToolsForLLM renders a compact tool catalog for prompt injection.
func FormatToolsForLLM(tools []ToolInfo) string {
	orderedTools := append([]ToolInfo(nil), tools...)
	sort.Slice(orderedTools, func(i, j int) bool {
		return orderedTools[i].Name < orderedTools[j].Name
	})

	var output strings.Builder
	for index, tool := range orderedTools {
		output.WriteString(fmt.Sprintf("%d. %s - %s\n", index+1, tool.Name, tool.Description))
		if len(tool.Parameters) == 0 {
			continue
		}

		output.WriteString("   参数:\n")
		parameterNames := make([]string, 0, len(tool.Parameters))
		for parameterName := range tool.Parameters {
			parameterNames = append(parameterNames, parameterName)
		}
		sort.Strings(parameterNames)

		for _, parameterName := range parameterNames {
			parameterDescription := tool.Parameters[parameterName]
			required := ""
			for _, requiredName := range tool.Required {
				if requiredName == parameterName {
					required = " (必填)"
					break
				}
			}
			output.WriteString(fmt.Sprintf("   - %s: %s%s\n", parameterName, parameterDescription, required))
		}
	}
	return output.String()
}
