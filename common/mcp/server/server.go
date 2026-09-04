// Package mcp hosts the MCP protocol boundary used by GopherAI DevSupport.
//
// The former weather, time, calculator, generic search, and page-fetch tools
// were intentionally retired because they did not serve the project knowledge
// and incident-diagnosis scenario. Scenario tools will be registered here only
// after they can pass the Tool Runtime authorization, budget, audit, and
// evaluation gates.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	serverName         = "gopherai-devsupport-tool-source"
	serverVersion      = "2.0.0"
	manifestSourceTool = "deployment_manifest_source"
	manifestLimit      = 64 * 1024
)

type publicManifest struct {
	ReleaseID          string   `json:"release_id"`
	Branch             string   `json:"branch"`
	GitSHA             string   `json:"git_sha"`
	SourceDirty        bool     `json:"source_dirty"`
	BuiltAt            string   `json:"built_at"`
	BuildStrategy      string   `json:"build_strategy"`
	Target             string   `json:"target"`
	GoVersion          string   `json:"go_version"`
	IncludedComponents []string `json:"included_components"`
	ConfigIncluded     bool     `json:"config_included"`
	Migrations         []string `json:"migrations"`
	Rollback           string   `json:"rollback"`
}

// NewMCPServer exposes only fixed, scenario-specific protocol sources. The
// public backend still has to call them through its governed Tool Runtime.
func NewMCPServer() *server.MCPServer {
	return NewMCPServerWithManifest("../../release-manifest.json")
}

func NewMCPServerWithManifest(manifestPath string) *server.MCPServer {
	mcpServer := server.NewMCPServer(
		serverName,
		serverVersion,
		server.WithToolCapabilities(true),
		server.WithLogging(),
	)
	mcpServer.AddTool(mcp.NewTool(manifestSourceTool,
		mcp.WithDescription("Return an allowlisted deployment manifest from one fixed server-side file; no path arguments."),
	), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		encoded, err := readPublicManifest(ctx, manifestPath)
		if err != nil {
			return mcp.NewToolResultError("deployment manifest source unavailable"), nil
		}
		return mcp.NewToolResultText(string(encoded)), nil
	})
	return mcpServer
}

func StartServer(httpAddr string) error {
	mcpServer := NewMCPServer()
	httpServer := server.NewStreamableHTTPServer(mcpServer)
	log.Printf("GopherAI DevSupport MCP protocol host listening on %s/mcp (1 governed source; demo tools disabled)", httpAddr)
	return httpServer.Start(httpAddr)
}

func readPublicManifest(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open fixed manifest: %w", err)
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, manifestLimit+1))
	if err != nil || len(contents) > manifestLimit {
		return nil, errors.New("fixed manifest is unreadable or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var manifest publicManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode fixed manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("fixed manifest has trailing content")
	}
	if strings.TrimSpace(manifest.ReleaseID) == "" || strings.TrimSpace(manifest.Branch) == "" || strings.TrimSpace(manifest.GitSHA) == "" {
		return nil, errors.New("fixed manifest misses identity")
	}
	return json.Marshal(manifest)
}
