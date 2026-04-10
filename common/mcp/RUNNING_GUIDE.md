# 运行指南

## 如何编译和运行MCP程序

### 1. 运行服务器

在 `common/mcp` 目录下运行服务器：

```bash
cd /Users/kaitai/project/GopherAI-/common/mcp
go run main.go --mode server --http-addr :8080
```

### 2. 运行客户端

在另一个终端中运行客户端（需要先启动服务器）：

```bash
cd /Users/kaitai/project/GopherAI-/common/mcp
go run main.go --mode client --city 北京
```

### 3. 使用示例

#### 启动服务器（终端1）：
```bash
cd /Users/kaitai/project/GopherAI-/common/mcp
go run main.go --mode server --http-addr :8080
```

#### 启动客户端（终端2）：
```bash
cd /Users/kaitai/project/GopherAI-/common/mcp
go run main.go --mode client --city 广州
```

### 4. 使用包

在其他项目中使用MCP包：

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

### 5. 构建

构建整个mcp包：

```bash
cd /Users/kaitai/project/GopherAI-/common/mcp
go build ./...
```

### 6. 测试

运行测试（如果有）：

```bash
go test ./...
```

## 常见问题

### 问题1：连接失败
**解决方案**：确保服务器已经启动，并且客户端使用了正确的传输类型和地址。

### 问题2：依赖缺失
**解决方案**：运行 `go mod tidy` 来下载和更新依赖。

### 问题3：端口占用
**解决方案**：更改HTTP地址，例如 `--http-addr :9090`。

## 命令行选项

```
--mode string
    运行模式: server 或 client (default "")

--http-addr string
    HTTP服务器地址 (default ":8080")

--city string
    要查询天气的城市名称 (default "")
```

## 示例输出

```
$ go run main.go --mode server --http-addr :8080
启动MCP服务器...
HTTP MCP server listening on :8080/mcp

$ go run main.go --mode client --city 北京
正在初始化HTTP客户端...
正在初始化客户端...
连接到服务器: weather-query-server (版本 1.0.0)
正在执行健康检查...
服务器正常运行并响应
正在查询城市 北京 的天气...

天气查询结果:
城市: 北京
温度: 15.5°C
天气状况: 多云
湿度: 65%
风速: 12.3 km/h

客户端初始化成功。正在关闭...
```
