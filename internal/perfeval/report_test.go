package perfeval

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPercentilesUseInterpolatedBoundedValues(t *testing.T) {
	got := Percentiles([]float64{50, 10, 40, 20, 30})
	if got.P50MS != 30 || got.P95MS != 48 || got.P99MS != 49.6 {
		t.Fatalf("unexpected percentiles: %+v", got)
	}
}

func TestParsePrometheusSumsOnlyExactCounterFamilies(t *testing.T) {
	input := "gopherai_model_calls_total{purpose=\"chat\",model=\"chat\"} 2\n" +
		"gopherai_model_calls_total{purpose=\"intent\",model=\"intent\"} 3\n" +
		"gopherai_model_calls_created 999\n" +
		"gopherai_model_tokens_total{direction=\"input\"} 40\n" +
		"gopherai_model_tokens_total{direction=\"output\"} 10\n" +
		"process_start_time_seconds 1000\n"
	got, err := ParsePrometheus(strings.NewReader(input))
	if err != nil || got.ModelCalls != 5 || got.Tokens != 50 || got.ProcessStart != 1000 || got.ModelCallsByAlias["chat"] != 2 || got.ModelCallsByAlias["intent"] != 3 {
		t.Fatalf("unexpected prometheus parse: %+v, %v", got, err)
	}
}

func TestScanStreamEventsRequiresDeltaAndFinal(t *testing.T) {
	callbacks := 0
	delta, final, route, err := ScanStreamEvents(strings.NewReader("event: meta\ndata: {}\n\nevent: delta\ndata: {}\n\nevent: final\ndata: {\"strategy\":\"legacy_chat\",\"strategy_version\":\"v1\",\"policy_version\":\"p1\"}\n\n"), func() { callbacks++ })
	if err != nil || !delta || !final || callbacks != 1 || route.Strategy != "legacy_chat" {
		t.Fatalf("unexpected stream scan: delta=%t final=%t callbacks=%d err=%v", delta, final, callbacks, err)
	}
}

func TestValidateConfigRejectsUnboundedLoadAndExternalPaths(t *testing.T) {
	base := Config{HotRequests: 10, HotConcurrency: 2, RequestTimeout: 30 * time.Second, ProfileSeconds: 5, OutputRoot: "/root/GopherAI_Runtime/perf", Token: "signed", Cleanup: func(context.Context) error { return nil }}
	if err := ValidateConfig(base); err != nil {
		t.Fatal(err)
	}
	bad := base
	bad.HotRequests = 49
	if err := ValidateConfig(bad); err == nil {
		t.Fatal("expected request bound rejection")
	}
	bad = base
	bad.OutputRoot = "/tmp/arbitrary"
	if err := ValidateConfig(bad); err == nil {
		t.Fatal("expected output root rejection")
	}
}

func TestFinalizeWriteAndStrictLoad(t *testing.T) {
	root := t.TempDir()
	profileRoot := "/root/GopherAI_Runtime/perf/release-1/"
	report := Report{
		SchemaVersion: SchemaVersion, RunnerVersion: RunnerVersion, GeneratedAt: time.Unix(100, 0).UTC(), Mode: "bounded_loopback_acceptance",
		Release: Release{ID: "release-1", GitSHA: strings.Repeat("a", 40), BuildStrategy: "local"},
		Runtime: Runtime{Target: chatTarget, GoVersion: "go1.test", CPUCores: 2, MemoryTotalBytes: 1},
		Cold:    NewPhase("release_first_request", "first", 1, 1, 0, []float64{10}, []float64{5}, 1, 10),
		Hot:     NewPhase("warmed_route", "warm", 2, 1, 1, []float64{8, 9}, []float64{3, 4}, 2, 20),
		Profiles: []ProfileArtifact{
			{Kind: "cpu", Path: profileRoot + "cpu.pprof", Bytes: 1, SHA256: strings.Repeat("a", 64)},
			{Kind: "heap", Path: profileRoot + "heap.pprof", Bytes: 1, SHA256: strings.Repeat("b", 64)},
			{Kind: "goroutine", Path: profileRoot + "goroutine.txt", Bytes: 1, SHA256: strings.Repeat("c", 64)},
		},
		Gates:      Gates{ColdEligible: true, AllRequestsPassed: true, ProfilesCaptured: true, SyntheticDataCleaned: true, TechnicalPassed: true},
		Guardrails: []string{"a", "b", "c", "d"}, Limitations: []string{"one", "two"},
	}
	report.Cold.DurationMS, report.Hot.DurationMS = 10, 17
	report.Cold.ObservedRoutes = []Route{{Strategy: "legacy_chat", StrategyVersion: "v1", PolicyVersion: "p1"}}
	report.Hot.ObservedRoutes = []Route{{Strategy: "legacy_chat", StrategyVersion: "v1", PolicyVersion: "p1"}}
	if err := Finalize(&report); err != nil {
		t.Fatal(err)
	}
	if err := WriteReports(root, report); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadReport(filepath.Join(root, "latest.json"))
	if err != nil || loaded.ReportSHA256 != report.ReportSHA256 {
		t.Fatalf("load failed: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "latest.json"))
	if bytes.Contains(data, []byte("Bearer")) || bytes.Contains(data, []byte("PERFORMANCE-PROBE")) {
		t.Fatal("report persisted sensitive request material")
	}
	tampered := append([]byte(nil), data...)
	tampered = bytes.Replace(tampered, []byte(`"duration_ms": 10`), []byte(`"duration_ms": 11`), 1)
	if bytes.Equal(tampered, data) {
		t.Fatal("test fixture did not alter the report")
	}
	if err := os.WriteFile(filepath.Join(root, "latest.json"), tampered, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReport(filepath.Join(root, "latest.json")); err == nil {
		t.Fatal("expected tampered report hash rejection")
	}
	if err := os.WriteFile(filepath.Join(root, "latest.json"), append(data, []byte(`{"extra":true}`)...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReport(filepath.Join(root, "latest.json")); err == nil {
		t.Fatal("expected trailing JSON rejection")
	}
}
