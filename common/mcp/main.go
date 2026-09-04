package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	mcpserver "github.com/kaitai/gopherai-mcp/server"
)

func main() {
	// 定义命令行标志
	mode := flag.String("mode", "", "运行模式: server")
	httpAddr := flag.String("http-addr", "127.0.0.1:8081", "仅容器回环可访问的 HTTP 地址")
	flag.Parse()

	if *mode == "" {
		fmt.Println("Error: 您必须使用 --mode server 启动 MCP 协议宿主")
		flag.Usage()
		os.Exit(1)
	}

	if *mode != "server" {
		fmt.Printf("Error: 不支持的模式 %q；旧 demo client 已退役\n", *mode)
		os.Exit(1)
	}

	fmt.Println("启动 GopherAI DevSupport MCP 协议宿主（demo 工具已禁用）...")
	if err := mcpserver.StartServer(*httpAddr); err != nil {
		log.Fatalf("服务器错误: %v", err)
	}
}
