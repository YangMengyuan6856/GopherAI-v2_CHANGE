package metriccatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	SchemaVersion          = "metric-catalog-audit-v1"
	CatalogVersion         = "gopherai-metric-catalog-v1"
	maximumLabelsPerMetric = 4
	maximumSeriesPerMetric = 10_000
	maximumCatalogSeries   = 100_000
)

type CollectorSet struct {
	Component  string
	Collectors []prometheus.Collector
}

type MetricDefinition struct {
	Name              string   `json:"name"`
	Type              string   `json:"type"`
	Component         string   `json:"component"`
	Domain            string   `json:"domain"`
	Help              string   `json:"help"`
	Labels            []string `json:"labels"`
	LabelPolicy       string   `json:"label_policy"`
	MaxSeriesEstimate int      `json:"max_series_estimate"`
	Required          bool     `json:"required"`
}

type CatalogViolation struct {
	Metric string `json:"metric,omitempty"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type CatalogGroupSummary struct {
	Name              string `json:"name"`
	FamilyCount       int    `json:"family_count"`
	MaxSeriesEstimate int    `json:"max_series_estimate"`
}

type CatalogReport struct {
	SchemaVersion          string                `json:"schema_version"`
	CatalogVersion         string                `json:"catalog_version"`
	CatalogSHA256          string                `json:"catalog_sha256"`
	Passed                 bool                  `json:"passed"`
	FamilyCount            int                   `json:"family_count"`
	LabelKeyCount          int                   `json:"label_key_count"`
	MaxSeriesEstimate      int                   `json:"max_series_estimate"`
	SeriesBudget           int                   `json:"series_budget"`
	ForbiddenLabelHits     int                   `json:"forbidden_label_hits"`
	DuplicateMetricNames   int                   `json:"duplicate_metric_names"`
	RequiredFamilyCount    int                   `json:"required_family_count"`
	RequiredPresentCount   int                   `json:"required_present_count"`
	ContractMismatchCount  int                   `json:"contract_mismatch_count"`
	MissingRequired        []string              `json:"missing_required"`
	Components             []CatalogGroupSummary `json:"components"`
	Domains                []CatalogGroupSummary `json:"domains"`
	Definitions            []MetricDefinition    `json:"definitions"`
	Violations             []CatalogViolation    `json:"violations"`
	HighCardinalityBlocked []string              `json:"high_cardinality_blocked"`
	Limitations            []string              `json:"limitations"`
}

var (
	descriptorNamePattern   = regexp.MustCompile(`fqName: "([^"]+)"`)
	descriptorHelpPattern   = regexp.MustCompile(`help: ("(?:\\.|[^"])*")`)
	descriptorLabelsPattern = regexp.MustCompile(`variableLabels: \{([^}]*)\}`)
	labelValueBudgets       = map[string]int{
		"intent": 7, "strategy": 8, "status": 16, "final_stage": 6, "stage": 6,
		"outcome": 8, "legacy_route": 8, "new_intent": 7, "agent": 4, "record_type": 6,
		"mode": 4, "gate_reason": 5, "enhancement": 3, "component": 6, "strength": 3,
		"decision": 4, "reason": 16, "from_state": 10, "to_state": 10, "state": 10,
		"resource": 5, "tool": 7, "result": 8, "entry": 2, "source": 3,
		"policy_version_short": 2, "error_code": 16,
		"purpose": 5, "model": 4, "direction": 2, "predicted_intent": 7, "verification": 3,
		"budget_type": 5, "from": 10, "to": 10, "segment": 7, "field": 8, "tier": 3,
		"type": 5, "dimension": 5, "suite": 8, "metric": 8, "action": 8, "event_type": 6,
	}
	blockedLabels = []string{
		"tenant_id", "user_id", "session_id", "request_id", "trace_id", "run_id", "step_id",
		"call_id", "document_id", "case_id", "prompt", "query", "path", "url", "email", "ip",
	}
	requiredMetricContracts = map[string]metricContract{
		"gopherai_requests_total":                    {metricType: "counter", labels: []string{"intent", "strategy", "status"}},
		"gopherai_request_duration_seconds":          {metricType: "histogram", labels: []string{"intent", "strategy"}},
		"gopherai_ttft_seconds":                      {metricType: "histogram", labels: []string{"strategy"}},
		"gopherai_model_calls_total":                 {metricType: "counter", labels: []string{"purpose", "model", "status"}},
		"gopherai_model_tokens_total":                {metricType: "counter", labels: []string{"purpose", "model", "direction"}},
		"gopherai_model_cost_micros_total":           {metricType: "counter", labels: []string{"purpose", "model"}},
		"gopherai_intent_decisions_total":            {metricType: "counter", labels: []string{"intent", "final_stage", "status"}},
		"gopherai_intent_confidence":                 {metricType: "histogram", labels: []string{"intent", "final_stage"}},
		"gopherai_intent_stage_duration_seconds":     {metricType: "histogram", labels: []string{"stage"}},
		"gopherai_intent_shadow_disagreements_total": {metricType: "counter", labels: []string{"legacy_route", "new_intent"}},
		"gopherai_intent_clarifications_total":       {metricType: "counter", labels: []string{"predicted_intent"}},
		"gopherai_rag_queries_total":                 {metricType: "counter", labels: []string{"strategy", "status"}},
		"gopherai_rag_duration_seconds":              {metricType: "histogram", labels: []string{"stage", "strategy"}},
		"gopherai_rag_candidates":                    {metricType: "histogram", labels: []string{"source"}},
		"gopherai_rag_top_score":                     {metricType: "histogram", labels: []string{"strategy"}},
		"gopherai_rag_empty_total":                   {metricType: "counter", labels: []string{"strategy", "reason"}},
		"gopherai_rag_rewrite_total":                 {metricType: "counter", labels: []string{"result"}},
		"gopherai_rag_rerank_total":                  {metricType: "counter", labels: []string{"result"}},
		"gopherai_citations_total":                   {metricType: "counter", labels: []string{"verification"}},
		"gopherai_agent_runs_total":                  {metricType: "counter", labels: []string{"agent", "strategy", "status"}},
		"gopherai_agent_duration_seconds":            {metricType: "histogram", labels: []string{"agent", "strategy"}},
		"gopherai_agent_budget_exceeded_total":       {metricType: "counter", labels: []string{"budget_type"}},
		"gopherai_agent_run_transitions_total":       {metricType: "counter", labels: []string{"from", "to", "result"}},
		"gopherai_agent_resume_total":                {metricType: "counter", labels: []string{"result"}},
		"gopherai_agent_no_progress_total":           {metricType: "counter", labels: []string{"agent", "strategy"}},
		"gopherai_agent_active_runs":                 {metricType: "gauge", labels: []string{"state"}},
		"gopherai_context_tokens":                    {metricType: "histogram", labels: []string{"segment", "direction"}},
		"gopherai_context_retention_checks_total":    {metricType: "counter", labels: []string{"field", "result"}},
		"gopherai_memory_recall_total":               {metricType: "counter", labels: []string{"tier", "status"}},
		"gopherai_memory_candidates":                 {metricType: "histogram", labels: []string{"tier"}},
		"gopherai_tool_calls_total":                  {metricType: "counter", labels: []string{"tool", "strategy", "status"}},
		"gopherai_tool_duration_seconds":             {metricType: "histogram", labels: []string{"tool", "strategy"}},
		"gopherai_tool_retries_total":                {metricType: "counter", labels: []string{"tool", "reason"}},
		"gopherai_tool_circuit_state":                {metricType: "gauge", labels: []string{"tool", "state"}},
		"gopherai_tool_cache_total":                  {metricType: "counter", labels: []string{"tool", "result"}},
		"gopherai_tool_validation_total":             {metricType: "counter", labels: []string{"tool", "result"}},
		"gopherai_tool_cancellations_total":          {metricType: "counter", labels: []string{"tool", "reason"}},
		"gopherai_feedback_total":                    {metricType: "counter", labels: []string{"strategy", "type", "result"}},
		"gopherai_online_eval_score":                 {metricType: "histogram", labels: []string{"strategy", "dimension"}},
		"gopherai_online_eval_failures_total":        {metricType: "counter", labels: []string{"strategy", "reason"}},
		"gopherai_eval_regressions_total":            {metricType: "counter", labels: []string{"suite", "metric"}},
		"gopherai_strategy_weight":                   {metricType: "gauge", labels: []string{"intent", "strategy", "policy_version_short"}},
		"gopherai_strategy_state":                    {metricType: "gauge", labels: []string{"intent", "strategy", "state"}},
		"gopherai_control_actions_total":             {metricType: "counter", labels: []string{"action", "result"}},
		"gopherai_control_loop_duration_seconds":     {metricType: "histogram", labels: []string{"result"}},
		"gopherai_webhook_deliveries_total":          {metricType: "counter", labels: []string{"event_type", "status"}},
	}
)

type metricContract struct {
	metricType string
	labels     []string
}

func Audit(sets ...CollectorSet) (CatalogReport, error) {
	if len(sets) == 0 {
		return CatalogReport{}, errors.New("at least one collector set is required")
	}
	report := CatalogReport{
		SchemaVersion: SchemaVersion, CatalogVersion: CatalogVersion, SeriesBudget: maximumCatalogSeries,
		Definitions: []MetricDefinition{}, Violations: []CatalogViolation{}, MissingRequired: []string{},
		RequiredFamilyCount:    len(requiredMetricContracts),
		HighCardinalityBlocked: append([]string(nil), blockedLabels...),
		Limitations: []string{
			"目录从应用与 Index Worker 实际注册的 Collector 描述生成，不读取调用方提供的指标名。",
			"最大序列数按固定标签值预算保守估算；生产实际活跃序列将在 Prometheus recording rules 接线后单独观测。",
			"通过目录审计只证明指标命名和标签边界合格，不等同于质量指标健康。",
		},
	}
	for _, set := range sets {
		component := strings.TrimSpace(set.Component)
		if component == "" {
			return CatalogReport{}, errors.New("collector component is required")
		}
		for _, collector := range set.Collectors {
			definitions, violations := describeCollector(component, collector)
			report.Definitions = append(report.Definitions, definitions...)
			report.Violations = append(report.Violations, violations...)
		}
	}
	sort.Slice(report.Definitions, func(i, j int) bool { return report.Definitions[i].Name < report.Definitions[j].Name })
	report.auditDefinitions()
	encoded, err := json.Marshal(report.Definitions)
	if err != nil {
		return CatalogReport{}, fmt.Errorf("encode metric catalog: %w", err)
	}
	digest := sha256.Sum256(encoded)
	report.CatalogSHA256 = hex.EncodeToString(digest[:])
	report.Passed = len(report.Violations) == 0 && report.FamilyCount > 0 && report.RequiredPresentCount == report.RequiredFamilyCount && report.MaxSeriesEstimate <= report.SeriesBudget
	return report, nil
}

func describeCollector(component string, collector prometheus.Collector) ([]MetricDefinition, []CatalogViolation) {
	if collector == nil {
		return nil, []CatalogViolation{{Code: "nil_collector", Detail: "collector must not be nil"}}
	}
	descriptions := make(chan *prometheus.Desc, 32)
	collector.Describe(descriptions)
	close(descriptions)
	definitions := []MetricDefinition{}
	violations := []CatalogViolation{}
	for descriptor := range descriptions {
		encoded := descriptor.String()
		nameMatch := descriptorNamePattern.FindStringSubmatch(encoded)
		if len(nameMatch) != 2 {
			violations = append(violations, CatalogViolation{Code: "invalid_descriptor", Detail: "collector descriptor has no metric name"})
			continue
		}
		name := nameMatch[1]
		help := ""
		if helpMatch := descriptorHelpPattern.FindStringSubmatch(encoded); len(helpMatch) == 2 {
			if decoded, err := strconv.Unquote(helpMatch[1]); err == nil {
				help = decoded
			}
		}
		labels := []string{}
		if labelMatch := descriptorLabelsPattern.FindStringSubmatch(encoded); len(labelMatch) == 2 {
			for _, label := range strings.Split(labelMatch[1], ",") {
				label = strings.TrimSpace(label)
				if label != "" {
					labels = append(labels, label)
				}
			}
		}
		definition := MetricDefinition{
			Name: name, Type: collectorType(collector), Component: component, Domain: metricDomain(name),
			Help: help, Labels: labels, LabelPolicy: "fixed_allowlist",
		}
		definition.MaxSeriesEstimate = estimateSeries(labels)
		definitions = append(definitions, definition)
	}
	if len(definitions) == 0 && len(violations) == 0 {
		violations = append(violations, CatalogViolation{Code: "unchecked_collector", Detail: "collector emitted no descriptors"})
	}
	return definitions, violations
}

func (report *CatalogReport) auditDefinitions() {
	seen := map[string]struct{}{}
	labelKeys := map[string]struct{}{}
	components := map[string]*CatalogGroupSummary{}
	domains := map[string]*CatalogGroupSummary{}
	for index := range report.Definitions {
		definition := &report.Definitions[index]
		contract, required := requiredMetricContracts[definition.Name]
		definition.Required = required
		if required {
			report.RequiredPresentCount++
			if definition.Type != contract.metricType || !equalLabelSet(definition.Labels, contract.labels) {
				report.ContractMismatchCount++
				report.Violations = append(report.Violations, CatalogViolation{Metric: definition.Name, Code: "required_contract_mismatch", Detail: fmt.Sprintf("want %s %v, got %s %v", contract.metricType, contract.labels, definition.Type, definition.Labels)})
			}
		}
		report.FamilyCount++
		report.MaxSeriesEstimate += definition.MaxSeriesEstimate
		if _, duplicate := seen[definition.Name]; duplicate {
			report.DuplicateMetricNames++
			report.Violations = append(report.Violations, CatalogViolation{Metric: definition.Name, Code: "duplicate_metric_name", Detail: "metric family name must be globally unique"})
		} else {
			seen[definition.Name] = struct{}{}
		}
		if !strings.HasPrefix(definition.Name, "gopherai_") {
			report.Violations = append(report.Violations, CatalogViolation{Metric: definition.Name, Code: "invalid_metric_prefix", Detail: "application metrics must use the gopherai_ prefix"})
		}
		if definition.Type == "unknown" {
			report.Violations = append(report.Violations, CatalogViolation{Metric: definition.Name, Code: "unknown_metric_type", Detail: "metric type is not catalogued"})
		}
		if len(definition.Labels) > maximumLabelsPerMetric {
			report.Violations = append(report.Violations, CatalogViolation{Metric: definition.Name, Code: "too_many_labels", Detail: fmt.Sprintf("%d labels exceeds the limit of %d", len(definition.Labels), maximumLabelsPerMetric)})
		}
		for _, label := range definition.Labels {
			labelKeys[label] = struct{}{}
			if _, allowed := labelValueBudgets[label]; !allowed {
				report.ForbiddenLabelHits++
				report.Violations = append(report.Violations, CatalogViolation{Metric: definition.Name, Code: "unbounded_label", Detail: "label " + label + " has no fixed value budget"})
			}
		}
		if definition.MaxSeriesEstimate > maximumSeriesPerMetric {
			report.Violations = append(report.Violations, CatalogViolation{Metric: definition.Name, Code: "family_series_budget_exceeded", Detail: fmt.Sprintf("estimated %d series exceeds %d", definition.MaxSeriesEstimate, maximumSeriesPerMetric)})
		}
		addGroup(components, definition.Component, definition.MaxSeriesEstimate)
		addGroup(domains, definition.Domain, definition.MaxSeriesEstimate)
	}
	for name := range requiredMetricContracts {
		if _, present := seen[name]; !present {
			report.MissingRequired = append(report.MissingRequired, name)
			report.Violations = append(report.Violations, CatalogViolation{Metric: name, Code: "required_metric_missing", Detail: "required SDD metric is not registered"})
		}
	}
	sort.Strings(report.MissingRequired)
	if report.MaxSeriesEstimate > report.SeriesBudget {
		report.Violations = append(report.Violations, CatalogViolation{Code: "catalog_series_budget_exceeded", Detail: fmt.Sprintf("estimated %d series exceeds %d", report.MaxSeriesEstimate, report.SeriesBudget)})
	}
	report.LabelKeyCount = len(labelKeys)
	report.Components = sortedGroups(components)
	report.Domains = sortedGroups(domains)
	sort.Slice(report.Violations, func(i, j int) bool {
		if report.Violations[i].Metric == report.Violations[j].Metric {
			return report.Violations[i].Code < report.Violations[j].Code
		}
		return report.Violations[i].Metric < report.Violations[j].Metric
	})
}

func equalLabelSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	a := append([]string(nil), left...)
	b := append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

func estimateSeries(labels []string) int {
	estimate := 1
	for _, label := range labels {
		budget, ok := labelValueBudgets[label]
		if !ok {
			return maximumSeriesPerMetric + 1
		}
		estimate *= budget
		if estimate > maximumSeriesPerMetric {
			return estimate
		}
	}
	return estimate
}

func collectorType(collector prometheus.Collector) string {
	switch collector.(type) {
	case *prometheus.HistogramVec, prometheus.Histogram:
		return "histogram"
	case *prometheus.GaugeVec, prometheus.Gauge:
		return "gauge"
	case *prometheus.CounterVec, prometheus.Counter:
		return "counter"
	default:
		return "unknown"
	}
}

func metricDomain(name string) string {
	switch {
	case strings.Contains(name, "intent_"):
		return "intent"
	case strings.Contains(name, "collaboration_"):
		return "multi_agent"
	case strings.Contains(name, "tool_"):
		return "tool_governance"
	case strings.Contains(name, "harness_") || strings.Contains(name, "agent_") || strings.Contains(name, "context_"):
		return "agent_harness"
	case strings.Contains(name, "memory_") || strings.Contains(name, "case_strategy"):
		return "memory"
	case strings.Contains(name, "rag_") || strings.Contains(name, "citation") || strings.Contains(name, "knowledge_") || strings.Contains(name, "document_") || strings.Contains(name, "index_") || strings.Contains(name, "outbox_"):
		return "knowledge_rag"
	case strings.Contains(name, "feedback_") || strings.Contains(name, "online_eval_") || strings.Contains(name, "eval_regressions"):
		return "evaluation"
	case strings.Contains(name, "policy_") || strings.Contains(name, "strategy_weight") || strings.Contains(name, "strategy_state") || strings.Contains(name, "control_") || strings.Contains(name, "webhook_"):
		return "control_plane"
	default:
		return "platform"
	}
}

func addGroup(groups map[string]*CatalogGroupSummary, name string, series int) {
	group := groups[name]
	if group == nil {
		group = &CatalogGroupSummary{Name: name}
		groups[name] = group
	}
	group.FamilyCount++
	group.MaxSeriesEstimate += series
}

func sortedGroups(groups map[string]*CatalogGroupSummary) []CatalogGroupSummary {
	result := make([]CatalogGroupSummary, 0, len(groups))
	for _, group := range groups {
		result = append(result, *group)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
