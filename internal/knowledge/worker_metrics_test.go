package knowledge

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestWorkerMetricsCollapseUntrustedLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewWorkerMetrics(registry)
	metrics.RecordIndexJob("case-id-123", "secret-error-456", time.Millisecond)
	metrics.RecordIncidentIndex("tenant-a", "trace-id-789", time.Millisecond)
	metrics.RecordOutboxPublish("request-42")
	if count := testutil.ToFloat64(metrics.indexJobs.WithLabelValues("unknown", "unknown")); count != 1 {
		t.Fatalf("untrusted index labels escaped bounds: %v", count)
	}
	if count := testutil.ToFloat64(metrics.incidentJobs.WithLabelValues("unknown", "unknown")); count != 1 {
		t.Fatalf("untrusted incident labels escaped bounds: %v", count)
	}
	if count := testutil.ToFloat64(metrics.outboxPublishes.WithLabelValues("unknown")); count != 1 {
		t.Fatalf("untrusted outbox label escaped bounds: %v", count)
	}
}

func TestWorkerMetricCollectorsStayComplete(t *testing.T) {
	metrics := NewWorkerMetrics(prometheus.NewRegistry())
	if count := len(metrics.Collectors()); count != 6 {
		t.Fatalf("expected 6 worker metric families, got %d", count)
	}
}
