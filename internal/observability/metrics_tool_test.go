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
