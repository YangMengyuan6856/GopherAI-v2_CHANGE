package observability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	GrafanaDashboardSchemaVersion = "grafana-dashboard-contract-v1"
	GrafanaRuntimeSchemaVersion   = "grafana-runtime-snapshot-v1"
	GrafanaDashboardUID           = "gopherai-closed-loop-v1"
	GrafanaDatasourceUID          = "gopherai-prometheus"
	ExpectedGrafanaVersion        = "13.2.1"

	grafanaDashboardRelativePath  = "deploy/observability/grafana/dashboards/gopherai-closed-loop.json"
	grafanaDatasourceRelativePath = "deploy/observability/grafana/provisioning/datasources/gopherai-prometheus.yml"
	grafanaProviderRelativePath   = "deploy/observability/grafana/provisioning/dashboards/gopherai.yml"
	maximumGrafanaResponseBytes   = 64 << 10
)

var expectedGrafanaGroups = []string{
	"1. 业务吞吐与可靠性",
	"2. RAG 与 Agent 质量",
	"3. 控制闭环与安全边界",
}

var forbiddenDashboardDimensions = []string{
	"tenant_id", "user_id", "session_id", "request_id", "trace_id", "run_id", "step_id", "call_id",
	"document_id", "case_id", "prompt", "query", "path", "url", "email", "ip",
}

type grafanaDatasourceRef struct {
	Type string `json:"type"`
	UID  string `json:"uid"`
}

type grafanaTarget struct {
	Expr  string `json:"expr"`
	RefID string `json:"refId"`
}

type grafanaPanel struct {
	ID          int                  `json:"id"`
	Type        string               `json:"type"`
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Datasource  grafanaDatasourceRef `json:"datasource"`
	Targets     []grafanaTarget      `json:"targets"`
}

type grafanaDashboardDocument struct {
	UID           string         `json:"uid"`
	Title         string         `json:"title"`
	Description   string         `json:"description"`
	Editable      bool           `json:"editable"`
	Refresh       string         `json:"refresh"`
	SchemaVersion int            `json:"schemaVersion"`
	Tags          []string       `json:"tags"`
	Panels        []grafanaPanel `json:"panels"`
}

type grafanaDatasourceProvisioning struct {
	APIVersion  int `yaml:"apiVersion"`
	Datasources []struct {
		Name      string `yaml:"name"`
		UID       string `yaml:"uid"`
		Type      string `yaml:"type"`
		Access    string `yaml:"access"`
		URL       string `yaml:"url"`
		IsDefault bool   `yaml:"isDefault"`
		Editable  bool   `yaml:"editable"`
	} `yaml:"datasources"`
}

type grafanaDashboardProvisioning struct {
	APIVersion int `yaml:"apiVersion"`
	Providers  []struct {
		Name            string `yaml:"name"`
		Type            string `yaml:"type"`
		DisableDeletion bool   `yaml:"disableDeletion"`
		AllowUIUpdates  bool   `yaml:"allowUiUpdates"`
		Options         struct {
			Path string `yaml:"path"`
		} `yaml:"options"`
	} `yaml:"providers"`
}

type GrafanaDashboardGroup struct {
	Title      string `json:"title"`
	PanelCount int    `json:"panel_count"`
}

type GrafanaDashboardContract struct {
	SchemaVersion string                  `json:"schema_version"`
	UID           string                  `json:"uid"`
	Title         string                  `json:"title"`
	DashboardSHA  string                  `json:"dashboard_sha256"`
	DatasourceUID string                  `json:"datasource_uid"`
	DatasourceURL string                  `json:"datasource_url"`
	Refresh       string                  `json:"refresh"`
	PanelCount    int                     `json:"panel_count"`
	QueryCount    int                     `json:"query_count"`
	Groups        []GrafanaDashboardGroup `json:"groups"`
	Passed        bool                    `json:"passed"`
}

type GrafanaRuntimeSnapshot struct {
	SchemaVersion  string                   `json:"schema_version"`
	Status         string                   `json:"status"`
	Source         string                   `json:"source"`
	CollectedAt    time.Time                `json:"collected_at"`
	GrafanaVersion string                   `json:"grafana_version"`
	Database       string                   `json:"database"`
	BindAddress    string                   `json:"bind_address"`
	PublicExposure bool                     `json:"public_exposure"`
	Dashboard      GrafanaDashboardContract `json:"dashboard"`
	Guardrails     []string                 `json:"guardrails"`
	Limitations    []string                 `json:"limitations"`
}

type GrafanaRuntimeClient struct {
	baseURL   *url.URL
	client    *http.Client
	assetRoot string
	clock     func() time.Time
}

func ValidateGrafanaAssets(root string) (GrafanaDashboardContract, error) {
	dashboardPath := filepath.Join(root, filepath.FromSlash(grafanaDashboardRelativePath))
	dashboardBytes, err := os.ReadFile(dashboardPath)
	if err != nil {
		return GrafanaDashboardContract{}, fmt.Errorf("read dashboard asset: %w", err)
	}
	var dashboard grafanaDashboardDocument
	if err := json.Unmarshal(dashboardBytes, &dashboard); err != nil {
		return GrafanaDashboardContract{}, fmt.Errorf("decode dashboard asset: %w", err)
	}
	if dashboard.UID != GrafanaDashboardUID || strings.TrimSpace(dashboard.Title) == "" || strings.TrimSpace(dashboard.Description) == "" || dashboard.Editable || dashboard.Refresh != "30s" || dashboard.SchemaVersion < 38 {
		return GrafanaDashboardContract{}, errors.New("dashboard metadata violated the fixed contract")
	}
	if !containsString(dashboard.Tags, "observe-only") || !containsString(dashboard.Tags, "production") {
		return GrafanaDashboardContract{}, errors.New("dashboard must disclose production and observe-only semantics")
	}
	groups := make(map[string]int, len(expectedGrafanaGroups))
	currentGroup := ""
	panelIDs := make(map[int]struct{}, len(dashboard.Panels))
	queryCount := 0
	panelCount := 0
	for _, panel := range dashboard.Panels {
		if panel.ID <= 0 {
			return GrafanaDashboardContract{}, errors.New("dashboard panel id must be positive")
		}
		if _, duplicate := panelIDs[panel.ID]; duplicate {
			return GrafanaDashboardContract{}, errors.New("dashboard panel ids must be unique")
		}
		panelIDs[panel.ID] = struct{}{}
		if panel.Type == "row" {
			if !containsString(expectedGrafanaGroups, panel.Title) {
				return GrafanaDashboardContract{}, fmt.Errorf("unexpected dashboard row %q", panel.Title)
			}
			currentGroup = panel.Title
			continue
		}
		if currentGroup == "" || strings.TrimSpace(panel.Title) == "" || strings.TrimSpace(panel.Description) == "" {
			return GrafanaDashboardContract{}, errors.New("dashboard panels must follow a documented group")
		}
		if panel.Datasource.Type != "prometheus" || panel.Datasource.UID != GrafanaDatasourceUID {
			return GrafanaDashboardContract{}, fmt.Errorf("panel %d bypassed the pinned datasource", panel.ID)
		}
		if len(panel.Targets) == 0 {
			return GrafanaDashboardContract{}, fmt.Errorf("panel %d has no query", panel.ID)
		}
		for _, target := range panel.Targets {
			if err := validateDashboardQuery(target.Expr, target.RefID); err != nil {
				return GrafanaDashboardContract{}, fmt.Errorf("panel %d: %w", panel.ID, err)
			}
			queryCount++
		}
		groups[currentGroup]++
		panelCount++
	}
	groupSummaries := make([]GrafanaDashboardGroup, 0, len(expectedGrafanaGroups))
	for _, group := range expectedGrafanaGroups {
		if groups[group] < 5 {
			return GrafanaDashboardContract{}, fmt.Errorf("dashboard group %q has only %d panels", group, groups[group])
		}
		groupSummaries = append(groupSummaries, GrafanaDashboardGroup{Title: group, PanelCount: groups[group]})
	}
	if panelCount < 18 || queryCount != panelCount {
		return GrafanaDashboardContract{}, errors.New("dashboard coverage is below the fixed contract")
	}

	datasource, err := loadGrafanaDatasource(filepath.Join(root, filepath.FromSlash(grafanaDatasourceRelativePath)))
	if err != nil {
		return GrafanaDashboardContract{}, err
	}
	if err := validateGrafanaProvider(filepath.Join(root, filepath.FromSlash(grafanaProviderRelativePath))); err != nil {
		return GrafanaDashboardContract{}, err
	}
	digest := sha256.Sum256(dashboardBytes)
	return GrafanaDashboardContract{
		SchemaVersion: GrafanaDashboardSchemaVersion,
		UID:           dashboard.UID, Title: dashboard.Title, DashboardSHA: hex.EncodeToString(digest[:]),
		DatasourceUID: datasource.UID, DatasourceURL: datasource.URL, Refresh: dashboard.Refresh,
		PanelCount: panelCount, QueryCount: queryCount, Groups: groupSummaries, Passed: true,
	}, nil
}

func loadGrafanaDatasource(path string) (struct{ UID, URL string }, error) {
	result := struct{ UID, URL string }{}
	data, err := os.ReadFile(path)
	if err != nil {
		return result, fmt.Errorf("read Grafana datasource provisioning: %w", err)
	}
	var provision grafanaDatasourceProvisioning
	if yaml.Unmarshal(data, &provision) != nil || provision.APIVersion != 1 || len(provision.Datasources) != 1 {
		return result, errors.New("Grafana datasource provisioning violated the fixed contract")
	}
	datasource := provision.Datasources[0]
	if datasource.UID != GrafanaDatasourceUID || datasource.Type != "prometheus" || datasource.Access != "proxy" || datasource.URL != "http://127.0.0.1:9092" || !datasource.IsDefault || datasource.Editable {
		return result, errors.New("Grafana datasource must be immutable and loopback-only")
	}
	result.UID, result.URL = datasource.UID, datasource.URL
	return result, nil
}

func validateGrafanaProvider(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Grafana dashboard provisioning: %w", err)
	}
	var provision grafanaDashboardProvisioning
	if yaml.Unmarshal(data, &provision) != nil || provision.APIVersion != 1 || len(provision.Providers) != 1 {
		return errors.New("Grafana dashboard provisioning violated the fixed contract")
	}
	provider := provision.Providers[0]
	if provider.Type != "file" || !provider.DisableDeletion || provider.AllowUIUpdates || provider.Options.Path != "/root/GopherAI-/deploy/observability/grafana/dashboards" {
		return errors.New("Grafana dashboard provider must use immutable release assets")
	}
	return nil
}

func validateDashboardQuery(expression string, refID string) error {
	expression = strings.TrimSpace(expression)
	if expression == "" || len(expression) > 1000 || strings.TrimSpace(refID) == "" || !strings.Contains(expression, "gopherai") {
		return errors.New("dashboard query violated the bounded PromQL contract")
	}
	lower := strings.ToLower(expression)
	for _, blocked := range forbiddenDashboardDimensions {
		if strings.Contains(lower, blocked) {
			return fmt.Errorf("dashboard query contains forbidden dimension %q", blocked)
		}
	}
	return nil
}

func NewGrafanaRuntimeClient(rawURL string, assetRoot string, client *http.Client) (*GrafanaRuntimeClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() == "" || !isLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("Grafana runtime URL must be HTTP loopback")
	}
	parsed.Path, parsed.RawQuery, parsed.Fragment = "", "", ""
	if strings.TrimSpace(assetRoot) == "" {
		return nil, errors.New("Grafana asset root is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	return &GrafanaRuntimeClient{baseURL: parsed, client: client, assetRoot: assetRoot, clock: time.Now}, nil
}

func NewDefaultGrafanaRuntimeClient() *GrafanaRuntimeClient {
	client, err := NewGrafanaRuntimeClient("http://127.0.0.1:9093", ".", nil)
	if err != nil {
		panic(err)
	}
	return client
}

func (client *GrafanaRuntimeClient) Snapshot(ctx context.Context) (GrafanaRuntimeSnapshot, error) {
	if client == nil || client.baseURL == nil || client.client == nil {
		return GrafanaRuntimeSnapshot{}, errors.New("Grafana runtime client is not configured")
	}
	contract, err := ValidateGrafanaAssets(client.assetRoot)
	if err != nil {
		return GrafanaRuntimeSnapshot{}, err
	}
	endpoint := *client.baseURL
	endpoint.Path = "/api/health"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return GrafanaRuntimeSnapshot{}, err
	}
	response, err := client.client.Do(request)
	if err != nil {
		return GrafanaRuntimeSnapshot{}, errors.New("Grafana loopback health check failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return GrafanaRuntimeSnapshot{}, fmt.Errorf("Grafana health returned HTTP %d", response.StatusCode)
	}
	var health struct {
		Database string `json:"database"`
		Version  string `json:"version"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, maximumGrafanaResponseBytes)).Decode(&health) != nil || health.Database != "ok" || strings.TrimSpace(health.Version) == "" {
		return GrafanaRuntimeSnapshot{}, errors.New("Grafana health response violated the fixed contract")
	}
	status := "ready"
	if health.Version != ExpectedGrafanaVersion {
		status = "version_mismatch"
	}
	return GrafanaRuntimeSnapshot{
		SchemaVersion: GrafanaRuntimeSchemaVersion, Status: status, Source: "grafana_loopback_health_and_provisioned_assets",
		CollectedAt: client.clock().UTC(), GrafanaVersion: health.Version, Database: health.Database,
		BindAddress: "container-private:9093", PublicExposure: false, Dashboard: contract,
		Guardrails:  []string{"Grafana 端口不发布到宿主机/公网", "Prometheus datasource 只读且不可编辑", "Dashboard 由 Release 文件供应", "PromQL 禁止请求级高基数维度"},
		Limitations: []string{"公网页面只展示脱敏运行摘要；完整 Grafana 仅允许通过 SSH 本地端口转发访问。", "面板展示观测与只建议结果，不代表系统已经自动调权或取得线上收益。"},
	}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func containsString(values []string, expected string) bool {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	index := sort.SearchStrings(sorted, expected)
	return index < len(sorted) && sorted[index] == expected
}
