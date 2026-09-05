package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestBoundedToolNameKeepsEveryGovernedProductionTool(t *testing.T) {
	tools := []string{
		"deployment_manifest_lookup",
		"service_health_snapshot",
		"bounded_log_signature",
		"mcp_deployment_evidence",
		"official_document_search",
		"confirm_resolution",
	}
	for _, tool := range tools {
		if got := boundedToolName(tool); got != tool {
			t.Fatalf("governed tool %q collapsed to metric label %q", tool, got)
		}
	}
	if got := boundedToolName("caller_controlled_tool"); got != "unknown" {
		t.Fatalf("unknown tool label must stay bounded, got %q", got)
	}
}

func TestLegacyEntryAttemptsUseFixedLowCardinalityLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry, registry)
	metrics.RecordLegacyEntryAttempt("skill_api")
	metrics.RecordLegacyEntryAttempt("/caller/controlled/path")
	if count := testutil.ToFloat64(metrics.legacyEntryAttempts.WithLabelValues("skill_api")); count != 1 {
		t.Fatalf("expected retired Skill request count 1, got %v", count)
	}
	if count := testutil.ToFloat64(metrics.legacyEntryAttempts.WithLabelValues("unknown")); count != 1 {
		t.Fatalf("expected unknown legacy label count 1, got %v", count)
	}
}

func TestRoutingPolicyMetricsUseFixedLowCardinalityLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry, registry)
	metrics.RecordPolicyLoad("redis", "success")
	metrics.RecordPolicyLoad("caller-source", "caller-result")
	metrics.SetStrategyWeight("routing-policy-v1", "rag_fast", 7500)
	metrics.SetStrategyWeight("caller-policy", "caller-strategy", 20000)
	if count := testutil.ToFloat64(metrics.policyLoads.WithLabelValues("redis", "success")); count != 1 {
		t.Fatalf("expected Redis policy load count 1, got %v", count)
	}
	if count := testutil.ToFloat64(metrics.policyLoads.WithLabelValues("mysql", "error")); count != 1 {
		t.Fatalf("untrusted policy labels escaped bounds: %v", count)
	}
	if weight := testutil.ToFloat64(metrics.strategyWeights.WithLabelValues("rag_fast", "routing-policy-v1")); weight != 7500 {
		t.Fatalf("unexpected strategy weight %v", weight)
	}
	if weight := testutil.ToFloat64(metrics.strategyWeights.WithLabelValues("unknown", "other")); weight != 10000 {
		t.Fatalf("untrusted strategy labels or weights escaped bounds: %v", weight)
	}
}

func TestCaseStrategyMetricsUseFixedLowCardinalityLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry, registry)
	metrics.RecordCaseStrategy("strong", "success", 25)
	metrics.RecordCaseStrategy("caller-strength", "caller-outcome", 50)
	if count := testutil.ToFloat64(metrics.caseStrategyRuns.WithLabelValues("strong", "success")); count != 1 {
		t.Fatalf("expected strong case strategy count 1, got %v", count)
	}
	if count := testutil.ToFloat64(metrics.caseStrategyRuns.WithLabelValues("none", "error")); count != 1 {
		t.Fatalf("untrusted case strategy labels escaped bounds: %v", count)
	}
}
