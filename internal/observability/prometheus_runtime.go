package observability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	PrometheusRuntimeSchemaVersion = "prometheus-runtime-snapshot-v1"
	RecordingRulesVersion          = "gopherai-recording-rules-v1"
	ExpectedPrometheusTargets      = 2
	ExpectedRecordingGroups        = 4
	ExpectedRecordingRules         = 17
	maximumPrometheusResponseBytes = 2 << 20
)

type PrometheusTargetSummary struct {
	Job          string    `json:"job"`
	Component    string    `json:"component"`
	Health       string    `json:"health"`
	LastScrapeAt time.Time `json:"last_scrape_at,omitempty"`
}

type PrometheusRuleGroupSummary struct {
	Name             string    `json:"name"`
	IntervalSeconds  float64   `json:"interval_seconds"`
	RuleCount        int       `json:"rule_count"`
	HealthyRuleCount int       `json:"healthy_rule_count"`
	FailedRuleCount  int       `json:"failed_rule_count"`
	LastEvaluationAt time.Time `json:"last_evaluation_at,omitempty"`
}

type PrometheusRuntimeSnapshot struct {
	SchemaVersion      string                       `json:"schema_version"`
	RulesVersion       string                       `json:"rules_version"`
	RulesSHA256        string                       `json:"rules_sha256"`
	Status             string                       `json:"status"`
	Source             string                       `json:"source"`
	CollectedAt        time.Time                    `json:"collected_at"`
	ExpectedTargets    int                          `json:"expected_targets"`
	TargetCount        int                          `json:"target_count"`
	HealthyTargetCount int                          `json:"healthy_target_count"`
	ExpectedGroups     int                          `json:"expected_groups"`
	GroupCount         int                          `json:"group_count"`
	ExpectedRules      int                          `json:"expected_rules"`
	RuleCount          int                          `json:"rule_count"`
	HealthyRuleCount   int                          `json:"healthy_rule_count"`
	FailedRuleCount    int                          `json:"failed_rule_count"`
	Targets            []PrometheusTargetSummary    `json:"targets"`
	Groups             []PrometheusRuleGroupSummary `json:"groups"`
	Guardrails         []string                     `json:"guardrails"`
	Limitations        []string                     `json:"limitations"`
}

type PrometheusRuntimeClient struct {
	baseURL *url.URL
	client  *http.Client
	clock   func() time.Time
}

func NewPrometheusRuntimeClient(rawURL string, client *http.Client) (*PrometheusRuntimeClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("invalid Prometheus base URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	return &PrometheusRuntimeClient{baseURL: parsed, client: client, clock: time.Now}, nil
}

func NewDefaultPrometheusRuntimeClient() *PrometheusRuntimeClient {
	client, err := NewPrometheusRuntimeClient("http://127.0.0.1:9092", nil)
	if err != nil {
		panic(err)
	}
	return client
}

func (client *PrometheusRuntimeClient) Snapshot(ctx context.Context) (PrometheusRuntimeSnapshot, error) {
	if client == nil || client.baseURL == nil || client.client == nil {
		return PrometheusRuntimeSnapshot{}, errors.New("Prometheus runtime client is not configured")
	}
	targets, err := client.targets(ctx)
	if err != nil {
		return PrometheusRuntimeSnapshot{}, fmt.Errorf("load Prometheus targets: %w", err)
	}
	groups, ruleNames, err := client.rules(ctx)
	if err != nil {
		return PrometheusRuntimeSnapshot{}, fmt.Errorf("load Prometheus recording rules: %w", err)
	}
	snapshot := PrometheusRuntimeSnapshot{
		SchemaVersion: PrometheusRuntimeSchemaVersion, RulesVersion: RecordingRulesVersion,
		Status: "ready", Source: "prometheus_http_api", CollectedAt: client.clock().UTC(),
		ExpectedTargets: ExpectedPrometheusTargets, ExpectedGroups: ExpectedRecordingGroups, ExpectedRules: ExpectedRecordingRules,
		Targets: targets, Groups: groups,
		Guardrails:  []string{"仅监听容器 loopback :9092", "只抓取 Backend/Index Worker", "72h/128MB 双保留上限", "请求级标识禁止进入聚合标签"},
		Limitations: []string{"当前快照证明 scrape 与 recording rules 正常，不等同于业务样本量已经满足异常检测门槛。", "Prometheus 进程在项目容器内运行，低内存 ECS 上必须继续观察 RSS 与 TSDB 大小。"},
	}
	for _, target := range targets {
		if target.Health == "up" {
			snapshot.HealthyTargetCount++
		}
	}
	for _, group := range groups {
		snapshot.RuleCount += group.RuleCount
		snapshot.HealthyRuleCount += group.HealthyRuleCount
		snapshot.FailedRuleCount += group.FailedRuleCount
	}
	snapshot.TargetCount = len(targets)
	snapshot.GroupCount = len(groups)
	sort.Strings(ruleNames)
	digest := sha256.Sum256([]byte(strings.Join(ruleNames, "\n")))
	snapshot.RulesSHA256 = hex.EncodeToString(digest[:])
	if snapshot.TargetCount == 0 || snapshot.GroupCount == 0 || snapshot.RuleCount == 0 {
		snapshot.Status = "warming"
	} else if snapshot.TargetCount != snapshot.ExpectedTargets || snapshot.HealthyTargetCount != snapshot.ExpectedTargets ||
		snapshot.GroupCount != snapshot.ExpectedGroups || snapshot.RuleCount != snapshot.ExpectedRules || snapshot.FailedRuleCount != 0 {
		snapshot.Status = "degraded"
	}
	return snapshot, nil
}

type prometheusTargetsData struct {
	ActiveTargets []struct {
		Labels     map[string]string `json:"labels"`
		Health     string            `json:"health"`
		LastScrape time.Time         `json:"lastScrape"`
	} `json:"activeTargets"`
}

func (client *PrometheusRuntimeClient) targets(ctx context.Context) ([]PrometheusTargetSummary, error) {
	var data prometheusTargetsData
	if err := client.get(ctx, "/api/v1/targets", url.Values{"state": []string{"active"}}, &data); err != nil {
		return nil, err
	}
	result := make([]PrometheusTargetSummary, 0, len(data.ActiveTargets))
	for _, target := range data.ActiveTargets {
		job := boundedPrometheusJob(target.Labels["job"])
		component := boundedPrometheusComponent(target.Labels["component"])
		if job == "unknown" || component == "unknown" {
			continue
		}
		result = append(result, PrometheusTargetSummary{Job: job, Component: component, Health: boundedPrometheusHealth(target.Health), LastScrapeAt: target.LastScrape.UTC()})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Job < result[j].Job })
	return result, nil
}

type prometheusRulesData struct {
	Groups []struct {
		Name           string    `json:"name"`
		Interval       float64   `json:"interval"`
		LastEvaluation time.Time `json:"lastEvaluation"`
		Rules          []struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			Health string `json:"health"`
		} `json:"rules"`
	} `json:"groups"`
}

func (client *PrometheusRuntimeClient) rules(ctx context.Context) ([]PrometheusRuleGroupSummary, []string, error) {
	var data prometheusRulesData
	if err := client.get(ctx, "/api/v1/rules", url.Values{"type": []string{"record"}}, &data); err != nil {
		return nil, nil, err
	}
	result := make([]PrometheusRuleGroupSummary, 0, len(data.Groups))
	ruleNames := []string{}
	for _, group := range data.Groups {
		if !strings.HasPrefix(group.Name, "gopherai-") {
			continue
		}
		summary := PrometheusRuleGroupSummary{Name: group.Name, IntervalSeconds: group.Interval, LastEvaluationAt: group.LastEvaluation.UTC()}
		for _, rule := range group.Rules {
			if rule.Type != "recording" || !strings.HasPrefix(rule.Name, "gopherai:") {
				continue
			}
			summary.RuleCount++
			ruleNames = append(ruleNames, group.Name+"/"+rule.Name)
			if rule.Health == "ok" {
				summary.HealthyRuleCount++
			} else {
				summary.FailedRuleCount++
			}
		}
		result = append(result, summary)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, ruleNames, nil
}

type prometheusAPIResponse struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Error  string          `json:"error"`
}

func (client *PrometheusRuntimeClient) get(ctx context.Context, path string, query url.Values, target any) error {
	endpoint := *client.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	response, err := client.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Prometheus returned HTTP %d", response.StatusCode)
	}
	var envelope prometheusAPIResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, maximumPrometheusResponseBytes)).Decode(&envelope); err != nil {
		return err
	}
	if envelope.Status != "success" {
		return errors.New("Prometheus API returned a non-success status")
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		return err
	}
	return nil
}

func boundedPrometheusJob(value string) string {
	switch value {
	case "gopherai-backend", "gopherai-index-worker":
		return value
	default:
		return "unknown"
	}
}

func boundedPrometheusComponent(value string) string {
	switch value {
	case "backend", "index_worker":
		return value
	default:
		return "unknown"
	}
}

func boundedPrometheusHealth(value string) string {
	if value == "up" {
		return "up"
	}
	return "down"
}
