package cleanupaudit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"GopherAI/internal/observability"
)

type fakeObservationReader struct {
	observation observability.LegacyEntryObservation
}

func (reader fakeObservationReader) ObserveRetiredSkillAPI(context.Context) (observability.LegacyEntryObservation, error) {
	return reader.observation, nil
}

func TestBuilderProducesBoundedDeletionPlan(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		".release-source-files.txt":                 "GopherAI\ncommon/mcp/gopherai-mcp\nuploads/example.md\n",
		"router/router.go":                          `LEGACY_SKILL_RETIRED`,
		"common/aihelper/tool_source.go":            `NewMCPToolSource NewCustomToolSource NewToolAggregator`,
		"common/mcp/client/client.go":               `package mcp`,
		"common/mcp/server/web_tools.go":            `func DuckDuckGoSearch() {} func FetchURLContent() {} func FormatSearchResults() {}`,
		"common/mcp/gopherai-mcp":                   `binary`,
		"config/config.go":                          `McpBaseURL string`,
		"common/mcp/main.go":                        `package main`,
		"common/mcp/server/server.go":               `deployment_manifest_source`,
		"internal/toolruntime/mcp_adapter_tool.go":  `mcp_deployment_evidence`,
		"scripts/deploy/deploy-aliyun.ps1":          `go build gopherai-mcp`,
		"internal/toolruntime/registry.go":          `type Registry struct{}`,
		"internal/toolruntime/official_document.go": `official_document_search`,
	}
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	observedAt := time.Date(2026, 9, 7, 1, 0, 0, 0, time.UTC)
	reader := fakeObservationReader{observation: observability.LegacyEntryObservation{
		SchemaVersion: "legacy-entry-observation-v1", Entry: "skill_api", WindowSeconds: 86400,
		ObservedAt: observedAt, AttemptCount: 0, SampleCount: 4000, ExpectedSampleCount: 5760,
		MinimumSampleCount: 2880, CoverageRatio: 4000.0 / 5760.0, ZeroCalls: true,
		CoverageSufficient: true, Status: "zero_calls_verified",
	}}
	report, err := NewBuilder(root, reader, func() time.Time { return observedAt }).Build(context.Background(), "release-1", "abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.AlreadyRemoved != 2 || report.Summary.EligibleToDelete != 7 || report.Summary.RetainedRequired != 2 || report.Summary.Blocked != 0 || !report.Summary.DeletionPlanReady || report.Summary.CleanupComplete {
		t.Fatalf("unexpected summary: %+v", report.Summary)
	}
	if err := Validate(report); err != nil {
		t.Fatal(err)
	}
}

func TestBuilderBlocksExternalReference(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		".release-source-files.txt":                "router/router.go\n",
		"common/aihelper/tool_source.go":           `NewToolAggregator`,
		"service/session/session.go":               `func f(){ NewToolAggregator() }`,
		"router/router.go":                         `LEGACY_SKILL_RETIRED`,
		"common/mcp/main.go":                       `package main`,
		"common/mcp/server/server.go":              `deployment_manifest_source`,
		"internal/toolruntime/mcp_adapter_tool.go": `mcp_deployment_evidence`,
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(contents), 0o644)
	}
	reader := fakeObservationReader{observation: observability.LegacyEntryObservation{
		SchemaVersion: "legacy-entry-observation-v1", Entry: "skill_api", WindowSeconds: 86400,
		ObservedAt: time.Now().UTC(), SampleCount: 4000, ExpectedSampleCount: 5760, MinimumSampleCount: 2880,
		CoverageSufficient: true, ZeroCalls: true, Status: "zero_calls_verified",
	}}
	report, err := NewBuilder(root, reader, time.Now).Build(context.Background(), "release-1", "abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Blocked != 1 || report.Summary.DeletionPlanReady {
		t.Fatalf("active reference must block deletion: %+v", report.Summary)
	}
}

func TestDeletionPlanAllowsIndependentSafeSubset(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		".release-source-files.txt":                "common/mcp/gopherai-mcp\n",
		"router/router.go":                         `LEGACY_SKILL_RETIRED`,
		"common/mcp/main.go":                       `package main`,
		"common/mcp/server/server.go":              `deployment_manifest_source`,
		"internal/toolruntime/mcp_adapter_tool.go": `mcp_deployment_evidence`,
		"common/mcp/gopherai-mcp":                  `binary`,
		"common/aihelper/tool_source.go":           `NewToolAggregator`,
		"service/session/session.go":               `func f(){ NewToolAggregator() }`,
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(contents), 0o644)
	}
	reader := fakeObservationReader{observation: observability.LegacyEntryObservation{
		SchemaVersion: "legacy-entry-observation-v1", Entry: "skill_api", WindowSeconds: 86400,
		ObservedAt: time.Now().UTC(), SampleCount: 4000, ExpectedSampleCount: 5760, MinimumSampleCount: 2880,
		CoverageSufficient: true, ZeroCalls: true, Status: "zero_calls_verified",
	}}
	report, err := NewBuilder(root, reader, time.Now).Build(context.Background(), "release-1", "abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Blocked != 1 || report.Summary.EligibleToDelete != 1 || !report.Summary.DeletionPlanReady || report.Summary.AllChecksPassed {
		t.Fatalf("safe subset should remain independently deletable: %+v", report.Summary)
	}
}

func TestFileStoreRejectsTampering(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		".release-source-files.txt":                "common/mcp/main.go\ncommon/mcp/server/server.go\ninternal/toolruntime/mcp_adapter_tool.go\nrouter/router.go\n",
		"router/router.go":                         `LEGACY_SKILL_RETIRED`,
		"common/mcp/main.go":                       `package main`,
		"common/mcp/server/server.go":              `deployment_manifest_source`,
		"internal/toolruntime/mcp_adapter_tool.go": `mcp_deployment_evidence`,
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(contents), 0o644)
	}
	reader := fakeObservationReader{observation: observability.LegacyEntryObservation{
		SchemaVersion: "legacy-entry-observation-v1", Entry: "skill_api", WindowSeconds: 86400,
		ObservedAt: time.Now().UTC(), SampleCount: 4000, ExpectedSampleCount: 5760, MinimumSampleCount: 2880,
		CoverageSufficient: true, ZeroCalls: true, Status: "zero_calls_verified",
	}}
	report, err := NewBuilder(root, reader, time.Now).Build(context.Background(), "release-1", "abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.AlreadyRemoved != 9 || report.Summary.RetainedRequired != 2 || report.Summary.EligibleToDelete != 0 || !report.Summary.AllChecksPassed || !report.Summary.CleanupComplete || report.Summary.DeletionPlanReady {
		t.Fatalf("fully retired candidates must produce a terminal cleanup state: %+v", report.Summary)
	}
	store := NewFileStore(filepath.Join(root, "reports", "cleanup.json"))
	if err := store.Save(report); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	encoded, _ := os.ReadFile(store.path)
	encoded[len(encoded)/2] ^= 1
	if err := os.WriteFile(store.path, encoded, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("tampered cleanup report must be rejected")
	}
}
