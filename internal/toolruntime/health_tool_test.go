package toolruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServiceHealthToolUsesFixedTargetAndReturnsAllowlistedSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/health/ready" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ready","service":"Backend","checked_at":"2026-09-05T05:30:00Z","components":{"mysql":{"status":"up","required":true,"latency_ms":2}}}`))
	}))
	defer server.Close()
	tool := NewServiceHealthTool()
	tool.endpoints["backend"] = server.URL
	output, err := tool.Execute(context.Background(), map[string]any{"service": "backend", "probe": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := output.Data.(PublicHealthSnapshot)
	if !ok || !snapshot.Healthy || snapshot.HTTPStatus != 200 || snapshot.Components["mysql"].Status != "up" {
		t.Fatalf("unexpected snapshot: %#v", output.Data)
	}
	if len(output.EvidenceRefs) != 1 || output.EvidenceRefs[0] != "health-probe:backend:ready" {
		t.Fatalf("unexpected evidence: %v", output.EvidenceRefs)
	}
}

func TestServiceHealthToolTreatsObservedUnhealthyAsSuccessfulObservation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte(`{"status":"not_ready","service":"Index Worker"}`))
	}))
	defer server.Close()
	tool := NewServiceHealthTool()
	tool.endpoints["index_worker"] = server.URL
	output, err := tool.Execute(context.Background(), map[string]any{"service": "index_worker", "probe": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := output.Data.(PublicHealthSnapshot)
	if snapshot.Healthy || snapshot.HTTPStatus != http.StatusServiceUnavailable || snapshot.Status != "not_ready" {
		t.Fatalf("unexpected unhealthy observation: %+v", snapshot)
	}
}

func TestServiceHealthToolRejectsTargetsOutsideAllowlist(t *testing.T) {
	tool := NewServiceHealthTool()
	if _, err := tool.Execute(context.Background(), map[string]any{"service": "http://attacker.invalid", "probe": "ready"}); err == nil {
		t.Fatal("arbitrary health target must be rejected")
	}
}
