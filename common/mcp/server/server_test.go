package mcp

import "testing"

func TestDemoToolsAreNotExposed(t *testing.T) {
	tools := NewMCPServer().ListTools()
	if len(tools) != 0 {
		t.Fatalf("expected no tools before governed scenario tools are implemented, got %d", len(tools))
	}

	for _, retiredName := range []string{"get_weather", "get_time", "calculate", "web_search", "fetch_url"} {
		if _, exists := tools[retiredName]; exists {
			t.Fatalf("retired demo tool %q is still exposed", retiredName)
		}
	}
}
