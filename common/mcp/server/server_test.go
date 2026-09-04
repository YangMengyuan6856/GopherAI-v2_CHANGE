package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnlyGovernedScenarioSourceIsExposed(t *testing.T) {
	tools := NewMCPServer().ListTools()
	if len(tools) != 1 {
		t.Fatalf("expected one governed source, got %d", len(tools))
	}
	if _, exists := tools[manifestSourceTool]; !exists {
		t.Fatalf("governed manifest source is missing: %v", tools)
	}

	for _, retiredName := range []string{"get_weather", "get_time", "calculate", "web_search", "fetch_url"} {
		if _, exists := tools[retiredName]; exists {
			t.Fatalf("retired demo tool %q is still exposed", retiredName)
		}
	}
}

func TestManifestSourceReturnsOnlyStrictAllowlistedShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release-manifest.json")
	payload := `{"release_id":"release-1","branch":"add_eico","git_sha":"abc123","source_dirty":false,"built_at":"2026-09-05T00:00:00Z","build_strategy":"local-cross-compile","target":"linux/amd64","go_version":"go1.25","included_components":["backend"],"config_included":false,"migrations":[],"rollback":"previous","secret":"must-fail"}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPublicManifest(context.Background(), path); err == nil {
		t.Fatal("unknown manifest field was accepted")
	}
	clean := strings.Replace(payload, `,"secret":"must-fail"`, "", 1)
	if err := os.WriteFile(path, []byte(clean), 0o600); err != nil {
		t.Fatal(err)
	}
	encoded, err := readPublicManifest(context.Background(), path)
	if err != nil || strings.Contains(string(encoded), "secret") || !strings.Contains(string(encoded), `"release_id":"release-1"`) {
		t.Fatalf("unexpected manifest source: %s err=%v", encoded, err)
	}
}
