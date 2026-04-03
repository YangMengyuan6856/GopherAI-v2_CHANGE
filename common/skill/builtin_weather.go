package skill

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

const WeatherSkillCode = "weather"

// WeatherSkill 天气查询技能，通过 MCP 协议调用 get_weather 工具实现
type WeatherSkill struct {
	mcpBaseURL string
}

// NewWeatherSkill 创建天气技能实例，mcpBaseURL 指向 MCP 服务地址（如 http://localhost:8081/mcp）
func NewWeatherSkill(mcpBaseURL string) *WeatherSkill {
	return &WeatherSkill{mcpBaseURL: mcpBaseURL}
}

func (w *WeatherSkill) Code() string        { return WeatherSkillCode }
func (w *WeatherSkill) Name() string        { return "天气查询" }
func (w *WeatherSkill) Description() string { return "查询指定城市的实时天气信息，示例：/skill weather 北京" }

// Execute 执行天气查询
func (w *WeatherSkill) Execute(ctx context.Context, req *ExecuteRequest) (*ExecuteResult, error) {
	// 优先取解析参数中的 city，否则尝试从 query 中识别
	city := req.Args["city"]
	if city == "" {
		city = extractCity(req.RawInput)
	}
	if city == "" {
		return &ExecuteResult{
			SkillCode: WeatherSkillCode,
			Output:    "请告诉我您想查询哪个城市的天气？示例：/skill weather 北京",
		}, nil
	}

	resultText, err := callMCPWeather(ctx, w.mcpBaseURL, city)
	if err != nil {
		return nil, fmt.Errorf("天气查询失败: %w", err)
	}

	return &ExecuteResult{
		SkillCode: WeatherSkillCode,
		Output:    resultText,
		Data:      map[string]interface{}{"city": city},
	}, nil
}

// callMCPWeather 通过 MCP 协议调用 get_weather 工具
func callMCPWeather(ctx context.Context, baseURL, city string) (string, error) {
	httpTransport, err := transport.NewStreamableHTTP(baseURL)
	if err != nil {
		return "", fmt.Errorf("create mcp transport failed: %w", err)
	}

	mcpClient := client.NewClient(httpTransport)

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "skill-weather-client",
		Version: "1.0.0",
	}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}

	if _, err = mcpClient.Initialize(ctx, initReq); err != nil {
		return "", fmt.Errorf("mcp initialize failed: %w", err)
	}
	defer mcpClient.Close()

	callReq := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "get_weather",
			Arguments: map[string]interface{}{"city": city},
		},
	}

	result, err := mcpClient.CallTool(ctx, callReq)
	if err != nil {
		return "", fmt.Errorf("mcp tool call failed: %w", err)
	}

	var sb strings.Builder
	for _, content := range result.Content {
		if tc, ok := content.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
			sb.WriteString("\n")
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// extractCity 从自然语言中识别常见城市名称
func extractCity(input string) string {
	cities := []string{
		"北京", "上海", "广州", "深圳", "杭州", "南京", "成都", "重庆", "武汉",
		"西安", "天津", "苏州", "长沙", "郑州", "东莞", "沈阳", "济南", "青岛",
		"宁波", "合肥", "昆明", "哈尔滨", "大连", "厦门", "福州", "贵阳",
		"Beijing", "Shanghai", "Guangzhou", "Shenzhen", "Hangzhou",
		"Nanjing", "Chengdu", "Chongqing", "Wuhan", "Xi'an",
	}
	lower := strings.ToLower(input)
	for _, city := range cities {
		if strings.Contains(lower, strings.ToLower(city)) {
			return city
		}
	}
	return ""
}
