package mcp

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

//wttr.in JSON 响应结构

type WttrResponse struct {
	CurrentCondition []struct {
		TempC         string `json:"temp_C"`
		Humidity      string `json:"humidity"`
		WindspeedKmph string `json:"windspeedKmph"`
		WeatherDesc   []struct {
			Value string `json:"value"`
		} `json:"weatherDesc"`
	} `json:"current_condition"`

	NearestArea []struct {
		AreaName []struct {
			Value string `json:"value"`
		} `json:"areaName"`
	} `json:"nearest_area"`
}

//统一对外天气结构

type WeatherResponse struct {
	Location    string  `json:"location"`
	Temperature float64 `json:"temperature"`
	Condition   string  `json:"condition"`
	Humidity    int     `json:"humidity"`
	WindSpeed   float64 `json:"windSpeed"`
}

//Weather API Client

type WeatherAPIClient struct{}

func NewWeatherAPIClient() *WeatherAPIClient {
	return &WeatherAPIClient{}
}

func (c *WeatherAPIClient) GetWeather(ctx context.Context, city string) (*WeatherResponse, error) {
	apiURL := fmt.Sprintf(
		"https://wttr.in/%s?format=j1&lang=zh",
		city,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "curl/7.88.1")

	client := &http.Client{
	Timeout: 15 * time.Second,
	Transport: &http.Transport{
	TLSNextProto: make(map[string]func(string, *tls.Conn) http.RoundTripper),
	},	
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}

	var wttrResp WttrResponse
	if err := json.Unmarshal(body, &wttrResp); err != nil {
		return nil, fmt.Errorf("json parse failed: %w", err)
	}

	if len(wttrResp.CurrentCondition) == 0 {
		return nil, fmt.Errorf("no weather data")
	}

	cc := wttrResp.CurrentCondition[0]

	temp, _ := strconv.ParseFloat(cc.TempC, 64)
	humidity, _ := strconv.Atoi(cc.Humidity)
	wind, _ := strconv.ParseFloat(cc.WindspeedKmph, 64)

	location := city
	if len(wttrResp.NearestArea) > 0 &&
		len(wttrResp.NearestArea[0].AreaName) > 0 {
		location = wttrResp.NearestArea[0].AreaName[0].Value
	}

	condition := "未知"
	if len(cc.WeatherDesc) > 0 {
		condition = cc.WeatherDesc[0].Value
	}

	return &WeatherResponse{
		Location:    location,
		Temperature: temp,
		Condition:   condition,
		Humidity:    humidity,
		WindSpeed:   wind,
	}, nil
}

/*
	========================
	MCP Server
	========================
*/

func NewMCPServer() *server.MCPServer {
	weatherClient := NewWeatherAPIClient()

	mcpServer := server.NewMCPServer(
		"weather-query-server",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithLogging(),
	)

	mcpServer.AddTool(
		mcp.NewTool(
			"get_weather",
			mcp.WithDescription("获取指定城市的天气信息"),
			mcp.WithString(
				"city",
				mcp.Description("城市名称，如 Beijing、上海"),
				mcp.Required(),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := request.GetArguments()
			city, ok := args["city"].(string)
			if !ok || city == "" {
				return nil, fmt.Errorf("invalid city argument")
			}

			weather, err := weatherClient.GetWeather(ctx, city)
			if err != nil {
				return nil, err
			}

			resultText := fmt.Sprintf(
				"城市: %s\n温度: %.1f°C\n天气: %s\n湿度: %d%%\n风速: %.1f km/h",
				weather.Location,
				weather.Temperature,
				weather.Condition,
				weather.Humidity,
				weather.WindSpeed,
			)

			return &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: resultText,
					},
				},
			}, nil
		},
	)

	// ── get_time 工具 ──
	mcpServer.AddTool(
		mcp.NewTool(
			"get_time",
			mcp.WithDescription("获取指定时区的当前时间，默认 Asia/Shanghai"),
			mcp.WithString(
				"timezone",
				mcp.Description("IANA 时区名称，如 Asia/Shanghai、America/New_York、Europe/London"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := request.GetArguments()
			tzName := "Asia/Shanghai"
			if tz, ok := args["timezone"].(string); ok && tz != "" {
				tzName = tz
			}

			loc, err := time.LoadLocation(tzName)
			if err != nil {
				return nil, fmt.Errorf("无效时区 %q: %w", tzName, err)
			}

			now := time.Now().In(loc)
			weekdayCN := [...]string{"日", "一", "二", "三", "四", "五", "六"}

			resultText := fmt.Sprintf(
				"时区: %s\n日期: %s\n时间: %s\n星期: 星期%s\nUnix时间戳: %d",
				tzName,
				now.Format("2006-01-02"),
				now.Format("15:04:05"),
				weekdayCN[now.Weekday()],
				now.Unix(),
			)

			return &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{Type: "text", Text: resultText},
				},
			}, nil
		},
	)

	// ── calculate 工具 ──
	mcpServer.AddTool(
		mcp.NewTool(
			"calculate",
			mcp.WithDescription("计算数学表达式，支持 +、-、*、/、() 和浮点数"),
			mcp.WithString(
				"expression",
				mcp.Description("数学表达式，如 (1+2)*3、3.14*10*10"),
				mcp.Required(),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := request.GetArguments()
			expr, ok := args["expression"].(string)
			if !ok || expr == "" {
				return nil, fmt.Errorf("请提供数学表达式")
			}

			expr = strings.ReplaceAll(expr, "×", "*")
			expr = strings.ReplaceAll(expr, "÷", "/")
			expr = strings.ReplaceAll(expr, "（", "(")
			expr = strings.ReplaceAll(expr, "）", ")")

			result, err := mcpEvalExpr(expr)
			if err != nil {
				return nil, fmt.Errorf("计算失败: %w", err)
			}

			formatted := strconv.FormatFloat(result, 'f', -1, 64)

			resultText := fmt.Sprintf("%s = %s", expr, formatted)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{Type: "text", Text: resultText},
				},
			}, nil
		},
	)

	return mcpServer
}

// ── calculate 辅助函数 ──

func mcpEvalExpr(expr string) (float64, error) {
	node, err := parser.ParseExpr(expr)
	if err != nil {
		return 0, fmt.Errorf("表达式语法错误: %w", err)
	}
	return mcpEvalNode(node)
}

func mcpEvalNode(node ast.Expr) (float64, error) {
	switch n := node.(type) {
	case *ast.BasicLit:
		if n.Kind == token.INT || n.Kind == token.FLOAT {
			return strconv.ParseFloat(n.Value, 64)
		}
		return 0, fmt.Errorf("不支持的字面量: %s", n.Value)
	case *ast.ParenExpr:
		return mcpEvalNode(n.X)
	case *ast.UnaryExpr:
		val, err := mcpEvalNode(n.X)
		if err != nil {
			return 0, err
		}
		if n.Op == token.SUB {
			return -val, nil
		}
		return val, nil
	case *ast.BinaryExpr:
		left, err := mcpEvalNode(n.X)
		if err != nil {
			return 0, err
		}
		right, err := mcpEvalNode(n.Y)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			if math.Abs(right) < 1e-15 {
				return 0, fmt.Errorf("除数不能为零")
			}
			return left / right, nil
		default:
			return 0, fmt.Errorf("不支持的运算符: %s", n.Op)
		}
	default:
		return 0, fmt.Errorf("不支持的表达式类型")
	}
}

// StartServer 启动MCP服务器
// httpAddr: HTTP服务器监听的地址（例如":8080"）
func StartServer(httpAddr string) error {
	mcpServer := NewMCPServer()

	httpServer := server.NewStreamableHTTPServer(mcpServer)
	log.Printf("HTTP MCP server listening on %s/mcp", httpAddr)
	return httpServer.Start(httpAddr)
}
