// Package mcp hosts the MCP protocol boundary used by GopherAI DevSupport.
//
// The former weather, time, calculator, generic search, and page-fetch tools
// were intentionally retired because they did not serve the project knowledge
// and incident-diagnosis scenario. Scenario tools will be registered here only
// after they can pass the Tool Runtime authorization, budget, audit, and
// evaluation gates.
package mcp

import (
	"log"

	"github.com/mark3labs/mcp-go/server"
)

const (
	serverName    = "gopherai-devsupport-tool-source"
	serverVersion = "2.0.0"
)

// NewMCPServer returns a protocol-capable server with no demo tools exposed.
// Keeping the protocol host alive lets the deployment topology remain stable
// while the governed scenario tools are implemented incrementally.
func NewMCPServer() *server.MCPServer {
	return server.NewMCPServer(
		serverName,
		serverVersion,
		server.WithToolCapabilities(true),
		server.WithLogging(),
	)
}

func StartServer(httpAddr string) error {
	mcpServer := NewMCPServer()
	httpServer := server.NewStreamableHTTPServer(mcpServer)
	log.Printf("GopherAI DevSupport MCP protocol host listening on %s/mcp (demo tools disabled)", httpAddr)
	return httpServer.Start(httpAddr)
}
