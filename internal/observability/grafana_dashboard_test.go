package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGrafanaDashboardAssetsPassVersionedContract(t *testing.T) {
	root := repositoryRoot(t)
	contract, err := ValidateGrafanaAssets(root)
	if err != nil {
		t.Fatal(err)
	}
	if !contract.Passed || contract.UID != GrafanaDashboardUID || contract.DatasourceUID != GrafanaDatasourceUID || contract.DatasourceURL != "http://127.0.0.1:9092" || contract.PanelCount < 18 || contract.QueryCount != contract.PanelCount || len(contract.Groups) != 3 || len(contract.DashboardSHA) != 64 {
		t.Fatalf("unexpected Grafana dashboard contract: %+v", contract)
	}
	for _, group := range contract.Groups {
		if group.PanelCount < 5 {
			t.Fatalf("dashboard group has insufficient coverage: %+v", group)
		}
	}
}

func TestGrafanaDashboardRejectsHighCardinalityPromQL(t *testing.T) {
	for _, expression := range []string{
		`sum by (user_id) (gopherai_requests_total)`,
		`gopherai_requests_total{trace_id="caller-controlled"}`,
		`rate(gopherai_tool_calls_total{url=~".*"}[5m])`,
	} {
		if err := validateDashboardQuery(expression, "A"); err == nil {
			t.Fatalf("query must be rejected: %s", expression)
		}
	}
}

func TestGrafanaRuntimeSnapshotCombinesHealthAndImmutableAssets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/health" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"database":"ok","version":"13.2.1","commit":"private"}`))
	}))
	defer server.Close()
	client, err := NewGrafanaRuntimeClient(server.URL, repositoryRoot(t), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.clock = func() time.Time { return time.Date(2026, 9, 6, 6, 0, 0, 0, time.UTC) }
	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "ready" || snapshot.GrafanaVersion != ExpectedGrafanaVersion || snapshot.Database != "ok" || snapshot.BindAddress != "container-private:9093" || snapshot.PublicExposure || snapshot.Dashboard.UID != GrafanaDashboardUID || snapshot.CollectedAt.IsZero() {
		t.Fatalf("unexpected Grafana runtime snapshot: %+v", snapshot)
	}
}

func TestGrafanaRuntimeRejectsPublicOrMalformedHealth(t *testing.T) {
	if _, err := NewGrafanaRuntimeClient("http://example.com:9093", ".", nil); err == nil {
		t.Fatal("public Grafana URL must be rejected")
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"database":"failed","version":"13.2.1","secret":"must-not-escape"}`))
	}))
	defer server.Close()
	client, err := NewGrafanaRuntimeClient(server.URL, repositoryRoot(t), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Snapshot(context.Background())
	if err == nil || strings.Contains(err.Error(), "must-not-escape") {
		t.Fatalf("health failure must be sanitized: %v", err)
	}
}

func TestGrafanaDashboardRejectsMutableProvisioning(t *testing.T) {
	root := t.TempDir()
	copyTreeForGrafanaTest(t, repositoryRoot(t), root)
	path := filepath.Join(root, filepath.FromSlash(grafanaProviderRelativePath))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "allowUiUpdates: false", "allowUiUpdates: true", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateGrafanaAssets(root); err == nil {
		t.Fatal("mutable dashboard provisioning must fail closed")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func copyTreeForGrafanaTest(t *testing.T, sourceRoot string, targetRoot string) {
	t.Helper()
	for _, relative := range []string{grafanaDashboardRelativePath, grafanaDatasourceRelativePath, grafanaProviderRelativePath} {
		source := filepath.Join(sourceRoot, filepath.FromSlash(relative))
		target := filepath.Join(targetRoot, filepath.FromSlash(relative))
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
