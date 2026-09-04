package toolruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const healthResponseLimit = 32 * 1024

type ServiceHealthTool struct {
	client    *http.Client
	endpoints map[string]string
}

type healthResponse struct {
	Status     string                     `json:"status"`
	Service    string                     `json:"service"`
	CheckedAt  string                     `json:"checked_at,omitempty"`
	Components map[string]healthComponent `json:"components,omitempty"`
}

type healthComponent struct {
	Status    string `json:"status"`
	Required  bool   `json:"required"`
	LatencyMS int64  `json:"latency_ms"`
}

type PublicHealthSnapshot struct {
	Target     string                     `json:"target"`
	Probe      string                     `json:"probe"`
	Healthy    bool                       `json:"healthy"`
	HTTPStatus int                        `json:"http_status"`
	Status     string                     `json:"status"`
	Service    string                     `json:"service"`
	CheckedAt  string                     `json:"checked_at,omitempty"`
	LatencyMS  int64                      `json:"latency_ms"`
	Components map[string]healthComponent `json:"components,omitempty"`
}

func NewServiceHealthTool() *ServiceHealthTool {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &ServiceHealthTool{
		client:    &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		endpoints: map[string]string{"backend": "http://127.0.0.1:9090", "index_worker": "http://127.0.0.1:9091"},
	}
}

func (tool *ServiceHealthTool) Definition() Definition {
	return Definition{
		Name: "service_health_snapshot", Version: "1.0.0",
		Description: "对固定 allowlist 中的 Backend 或 Index Worker 执行 live/ready 探测，返回受限健康快照；不接受 URL、主机或端口。",
		InputSchema: InputSchema{Type: "object", Properties: map[string]PropertySchema{
			"service": {Type: "string", Description: "固定服务标识", Enum: []string{"backend", "index_worker", "all"}, MinLength: 3, MaxLength: 12},
			"probe":   {Type: "string", Description: "固定健康探针", Enum: []string{"live", "ready"}, MinLength: 4, MaxLength: 5},
		}, Required: []string{"service", "probe"}, AdditionalProperties: false},
		AllowedIntents: []string{"tool_task", "troubleshooting"}, RequiredPermission: "devsupport:tools:read",
		SideEffect: SideEffectReadOnly, TimeoutMS: 1500, MaxResultBytes: 16384,
		Idempotent: true, RetryMaxAttempts: 2, CacheTTLMS: 750, CircuitFailures: 2, CircuitOpenMS: 3000,
	}
}

func (tool *ServiceHealthTool) Execute(ctx context.Context, arguments map[string]any) (Output, error) {
	service, _ := arguments["service"].(string)
	probe, _ := arguments["probe"].(string)
	if service == "all" {
		snapshots := make([]PublicHealthSnapshot, 0, 2)
		for _, target := range []string{"backend", "index_worker"} {
			output, err := tool.executeOne(ctx, target, probe)
			if err != nil {
				return output, err
			}
			snapshots = append(snapshots, output.Data.(PublicHealthSnapshot))
		}
		return Output{Data: map[string]any{"healthy": snapshots[0].Healthy && snapshots[1].Healthy, "snapshots": snapshots}, EvidenceRefs: []string{"health-probe:all:" + probe}}, nil
	}
	return tool.executeOne(ctx, service, probe)
}

func (tool *ServiceHealthTool) executeOne(ctx context.Context, service string, probe string) (Output, error) {
	baseURL, ok := tool.endpoints[service]
	if !ok || (probe != "live" && probe != "ready") {
		return Output{}, errors.New("health target is outside the fixed allowlist")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health/"+probe, nil)
	if err != nil {
		return Output{}, err
	}
	startedAt := time.Now()
	response, err := tool.client.Do(request)
	if err != nil {
		return Output{Retryable: true}, fmt.Errorf("health probe transport failed: %w", err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, healthResponseLimit+1))
	if err != nil {
		return Output{Retryable: true}, fmt.Errorf("health probe read failed: %w", err)
	}
	if len(contents) > healthResponseLimit {
		return Output{}, errors.New("health response exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	var payload healthResponse
	if err := decoder.Decode(&payload); err != nil {
		return Output{}, errors.New("health response is not valid JSON")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Output{}, err
	}
	snapshot := PublicHealthSnapshot{
		Target: service, Probe: probe, Healthy: response.StatusCode == http.StatusOK && (payload.Status == "ready" || payload.Status == "alive"),
		HTTPStatus: response.StatusCode, Status: payload.Status, Service: payload.Service, CheckedAt: payload.CheckedAt,
		LatencyMS: time.Since(startedAt).Milliseconds(), Components: payload.Components,
	}
	return Output{Data: snapshot, EvidenceRefs: []string{"health-probe:" + service + ":" + probe}}, nil
}
