package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrometheusRuntimeSnapshotRequiresHealthyExpectedTargetsAndRules(t *testing.T) {
	server := prometheusRuntimeServer(t, "up", 17)
	defer server.Close()
	client, err := NewPrometheusRuntimeClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.clock = func() time.Time { return time.Date(2026, 9, 6, 1, 0, 0, 0, time.UTC) }
	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "ready" || snapshot.TargetCount != 2 || snapshot.HealthyTargetCount != 2 || snapshot.GroupCount != 4 || snapshot.RuleCount != 17 || snapshot.HealthyRuleCount != 17 || snapshot.FailedRuleCount != 0 || len(snapshot.RulesSHA256) != 64 {
		t.Fatalf("unexpected runtime snapshot: %+v", snapshot)
	}
}

func TestPrometheusRuntimeSnapshotDegradesWithoutHidingFailedSignals(t *testing.T) {
	server := prometheusRuntimeServer(t, "down", 16)
	defer server.Close()
	client, err := NewPrometheusRuntimeClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "degraded" || snapshot.HealthyTargetCount != 0 || snapshot.RuleCount != 16 || snapshot.FailedRuleCount != 4 {
		t.Fatalf("degraded runtime evidence was hidden: %+v", snapshot)
	}
}

func TestPrometheusRuntimeClientRejectsInvalidURLAndAPIStatus(t *testing.T) {
	if _, err := NewPrometheusRuntimeClient("file:///tmp/prometheus", nil); err == nil {
		t.Fatal("non-HTTP Prometheus URL must be rejected")
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"error","error":"internal detail must not escape"}`))
	}))
	defer server.Close()
	client, _ := NewPrometheusRuntimeClient(server.URL, server.Client())
	_, err := client.Snapshot(context.Background())
	if err == nil || strings.Contains(err.Error(), "internal detail") {
		t.Fatalf("expected sanitized API failure, got %v", err)
	}
}

func prometheusRuntimeServer(t *testing.T, targetHealth string, rules int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/targets":
			_, _ = writer.Write([]byte(`{"status":"success","data":{"activeTargets":[` +
				`{"labels":{"job":"gopherai-backend","component":"backend"},"health":"` + targetHealth + `","lastScrape":"2026-09-06T00:00:00Z"},` +
				`{"labels":{"job":"gopherai-index-worker","component":"index_worker"},"health":"` + targetHealth + `","lastScrape":"2026-09-06T00:00:00Z"},` +
				`{"labels":{"job":"caller-controlled","component":"tenant-42"},"health":"up","lastScrape":"2026-09-06T00:00:00Z"}]}}`))
		case "/api/v1/rules":
			groupRules := rules / 4
			extra := rules % 4
			groups := []string{}
			for group := 0; group < 4; group++ {
				count := groupRules
				if group < extra {
					count++
				}
				items := []string{}
				for index := 0; index < count; index++ {
					health := "ok"
					if targetHealth == "down" && index == 0 {
						health = "err"
					}
					items = append(items, `{"name":"gopherai:test_`+string(rune('a'+group))+`_`+string(rune('a'+index))+`","type":"recording","health":"`+health+`"}`)
				}
				groups = append(groups, `{"name":"gopherai-test-`+string(rune('a'+group))+`","interval":15,"lastEvaluation":"2026-09-06T00:00:00Z","rules":[`+strings.Join(items, ",")+`]}`)
			}
			_, _ = writer.Write([]byte(`{"status":"success","data":{"groups":[` + strings.Join(groups, ",") + `]}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
}
