package toolruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const mcpManifestSourceName = "deployment_manifest_source"

type MCPTextInvoker interface {
	InvokeText(context.Context, string, map[string]any) ([]byte, error)
}

type MCPDeploymentEvidenceTool struct{ invoker MCPTextInvoker }

func NewMCPDeploymentEvidenceTool(baseURL string) *MCPDeploymentEvidenceTool {
	return &MCPDeploymentEvidenceTool{invoker: NewMCPClient(baseURL)}
}

func NewMCPDeploymentEvidenceToolWithInvoker(invoker MCPTextInvoker) *MCPDeploymentEvidenceTool {
	return &MCPDeploymentEvidenceTool{invoker: invoker}
}

func (tool *MCPDeploymentEvidenceTool) Definition() Definition {
	return Definition{
		Name: "mcp_deployment_evidence", Version: "1.0.0",
		Description:    "通过容器回环 MCP 协议读取固定部署证据源，再由统一 Runtime 执行权限、预算、超时、熔断、审计与结果校验。",
		InputSchema:    InputSchema{Type: "object", Properties: map[string]PropertySchema{}, AdditionalProperties: false},
		AllowedIntents: []string{"tool_task", "troubleshooting"}, RequiredPermission: "devsupport:tools:read",
		SideEffect: SideEffectReadOnly, TimeoutMS: 1500, MaxResultBytes: 8192,
		Idempotent: true, RetryMaxAttempts: 2, CacheTTLMS: 3000, CircuitFailures: 2, CircuitOpenMS: 5000,
	}
}

func (tool *MCPDeploymentEvidenceTool) Execute(ctx context.Context, _ map[string]any) (Output, error) {
	if tool == nil || tool.invoker == nil {
		return Output{}, errors.New("MCP adapter is unavailable")
	}
	contents, err := tool.invoker.InvokeText(ctx, mcpManifestSourceName, map[string]any{})
	if err != nil {
		return Output{Retryable: true}, fmt.Errorf("invoke MCP deployment source: %w", err)
	}
	if len(contents) > mcpTextResultLimit {
		return Output{}, errors.New("MCP deployment payload exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var manifest PublicDeploymentManifest
	if err := decoder.Decode(&manifest); err != nil {
		return Output{}, fmt.Errorf("decode MCP deployment payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Output{}, errors.New("MCP deployment payload has trailing content")
	}
	if strings.TrimSpace(manifest.ReleaseID) == "" || strings.TrimSpace(manifest.Branch) == "" || strings.TrimSpace(manifest.GitSHA) == "" {
		return Output{}, errors.New("MCP deployment payload misses identity")
	}
	return Output{Data: manifest, EvidenceRefs: []string{"mcp:" + mcpManifestSourceName + ":" + manifest.ReleaseID}}, nil
}
