package observability

import "testing"

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
