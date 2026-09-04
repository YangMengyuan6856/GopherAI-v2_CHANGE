package toolruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDeploymentManifestToolReturnsAllowlistedReleaseEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release-manifest.json")
	contents := `{"release_id":"20260905050000-abc","branch":"add_eico","git_sha":"abcdef123","source_dirty":false,"built_at":"2026-09-05T05:00:00Z","build_strategy":"container","target":"linux/amd64","go_version":"go1.24.10","included_components":["backend","mcp"],"config_included":false,"migrations":[],"rollback":"previous-directory"}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := NewDeploymentManifestTool(path).Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	manifest, ok := output.Data.(PublicDeploymentManifest)
	if !ok || manifest.ReleaseID != "20260905050000-abc" || manifest.GitSHA != "abcdef123" {
		t.Fatalf("unexpected manifest: %#v", output.Data)
	}
	if len(output.EvidenceRefs) != 1 || output.EvidenceRefs[0] != "release-manifest:20260905050000-abc" {
		t.Fatalf("unexpected evidence: %v", output.EvidenceRefs)
	}
}

func TestDeploymentManifestToolRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release-manifest.json")
	contents := `{"release_id":"r1","branch":"add_eico","git_sha":"abc","secret":"must-not-leak"}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDeploymentManifestTool(path).Execute(context.Background(), map[string]any{}); err == nil {
		t.Fatal("strict manifest decoder must reject unknown fields")
	}
}
