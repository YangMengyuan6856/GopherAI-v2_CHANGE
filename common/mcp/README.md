# MCP Package

This package provides MCP (Model Context Protocol) server and client implementations for the GopherAI project.

## Structure

```
common/mcp/
├── client/          # MCP Client implementation
│   └── client.go    # MCPClient struct and methods
├── server/          # MCP Server implementation
│   └── server.go    # Server functions and tools
├── go.mod           # Module definition
├── go.sum           # Dependencies checksum
└── README.md        # This file
```

## Server Package

The server package provides functionality to start an MCP server.

### Usage

```go
import "github.com/yourusername/gopherai/common/mcp/server"

// Start an HTTP server
if err := server.StartServer(":8080"); err != nil {
    log.Fatalf("Server error: %v", err)
}
```

### Functions

- `NewMCPServer()` - Creates a new MCP server instance
- `StartServer(httpAddr string) error` - Starts the MCP server
  - `httpAddr`: HTTP server address (e.g., ":8080")

## Client Package

The client package provides an MCPClient struct for interacting with MCP servers.

### Usage

```go
import (
    "context"
    "github.com/yourusername/gopherai/common/mcp/client"
)

// Create a client
mcpClient, err := client.NewMCPClient("http://localhost:8080/mcp")
if err != nil {
    log.Fatalf("Failed to create client: %v", err)
}
defer mcpClient.Close()

// Initialize the client
if _, err := mcpClient.Initialize(ctx); err != nil {
    log.Fatalf("Initialization failed: %v", err)
}

// Call a tool
result, err := mcpClient.CallTool(ctx, "get_weather", map[string]any{
    "city": "北京",
})
if err != nil {
    log.Fatalf("Tool call failed: %v", err)
}

// Get result text
text := mcpClient.GetToolResultText(result)
fmt.Println(text)
```

### Methods

- `NewMCPClient(httpURL string) (*MCPClient, error)` - Creates a new MCP client
  - `httpURL`: URL for HTTP transport

- `Initialize(ctx context.Context) (*mcp.InitializeResult, error)` - Initializes the client

- `Ping(ctx context.Context) error` - Performs a health check

- `CallTool(ctx context.Context, toolName string, args map[string]any) (*mcp.CallToolResult, error)` - Calls an MCP tool

- `CallWeatherTool(ctx context.Context, city string) (*mcp.CallToolResult, error)` - Convenience method for weather queries

- `GetToolResultText(result *mcp.CallToolResult) string` - Extracts text from tool result

- `Close()` - Closes the client connection

## Example

```go
import (
    "context"
    "github.com/yourusername/gopherai/common/mcp/client"
    "github.com/yourusername/gopherai/common/mcp/server"
)

// 启动服务器
server.StartServer(":8080")

// 创建客户端
mcpClient, _ := client.NewMCPClient("http://localhost:8080/mcp")
defer mcpClient.Close()

// 使用客户端
ctx := context.Background()
mcpClient.Initialize(ctx)
result, _ := mcpClient.CallWeatherTool(ctx, "深圳")
fmt.Println(mcpClient.GetToolResultText(result))
```

## Dependencies

- `github.com/mark3labs/mcp-go v0.43.2`
