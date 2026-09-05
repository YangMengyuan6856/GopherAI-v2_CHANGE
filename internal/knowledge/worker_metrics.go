package knowledge

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type WorkerMetrics struct {
	outboxPublishes  *prometheus.CounterVec
	outboxOldestAge  prometheus.Gauge
	indexJobs        *prometheus.CounterVec
	indexDuration    prometheus.Histogram
	incidentJobs     *prometheus.CounterVec
	incidentDuration prometheus.Histogram
}

// Collectors is shared by registration and metric catalog discovery.
func (metrics *WorkerMetrics) Collectors() []prometheus.Collector {
	if metrics == nil {
		return nil
	}
	return []prometheus.Collector{
		metrics.outboxPublishes,
		metrics.outboxOldestAge,
		metrics.indexJobs,
		metrics.indexDuration,
		metrics.incidentJobs,
		metrics.incidentDuration,
	}
}

func NewWorkerMetrics(registerer prometheus.Registerer) *WorkerMetrics {
	metrics := &WorkerMetrics{
		outboxPublishes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_outbox_publish_total",
			Help: "Total outbox publishing outcomes.",
		}, []string{"status"}),
		outboxOldestAge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gopherai_outbox_oldest_age_seconds",
			Help: "Age in seconds of the oldest currently publishable outbox event.",
		}),
		indexJobs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_index_jobs_total",
			Help: "Total knowledge indexing job outcomes.",
		}, []string{"status", "error_code"}),
		indexDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "gopherai_index_job_duration_seconds",
			Help:    "Knowledge indexing execution duration in seconds.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 40, 80},
		}),
		incidentJobs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_incident_index_jobs_total",
			Help: "Total confirmed incident indexing outcomes.",
		}, []string{"status", "error_code"}),
		incidentDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "gopherai_incident_index_job_duration_seconds",
			Help:    "Confirmed incident indexing execution duration in seconds.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
		}),
	}
	registerer.MustRegister(metrics.Collectors()...)
	for _, status := range []string{"published", "error", "invalid"} {
		metrics.outboxPublishes.WithLabelValues(status).Add(0)
	}
	for _, status := range []string{"success", "retry", "dead"} {
		metrics.indexJobs.WithLabelValues(status, "none").Add(0)
		metrics.incidentJobs.WithLabelValues(status, "none").Add(0)
	}
	return metrics
}

func (metrics *WorkerMetrics) RecordIncidentIndex(status string, code string, duration time.Duration) {
	metrics.incidentJobs.WithLabelValues(boundedWorkerJobStatus(status), boundedWorkerErrorCode(code)).Inc()
	metrics.incidentDuration.Observe(duration.Seconds())
}

func (metrics *WorkerMetrics) RecordOutboxPublish(status string) {
	metrics.outboxPublishes.WithLabelValues(boundedOutboxStatus(status)).Inc()
}

func (metrics *WorkerMetrics) SetOutboxOldestAge(seconds float64) {
	metrics.outboxOldestAge.Set(seconds)
}

func (metrics *WorkerMetrics) RecordIndexJob(status string, code string, duration time.Duration) {
	metrics.indexJobs.WithLabelValues(boundedWorkerJobStatus(status), boundedWorkerErrorCode(code)).Inc()
	metrics.indexDuration.Observe(duration.Seconds())
}

func boundedWorkerJobStatus(status string) string {
	switch status {
	case "success", "retry", "dead":
		return status
	default:
		return "unknown"
	}
}

func boundedOutboxStatus(status string) string {
	switch status {
	case "published", "error", "invalid":
		return status
	default:
		return "unknown"
	}
}

func boundedWorkerErrorCode(code string) string {
	switch code {
	case "":
		return "none"
	case "INDEX_EVENT_INVALID", "INDEX_JOB_NOT_FOUND", "INDEX_INTERNAL_ERROR", "DOCUMENT_STORAGE_READ_FAILED", "DOCUMENT_PARSE_FAILED",
		"CHUNK_PERSIST_FAILED", "REDIS_INDEX_FAILED", "INDEX_COMPLETION_FAILED", "REDIS_DELETE_FAILED",
		"DELETE_COMPLETION_FAILED", "INCIDENT_EVENT_INVALID", "INCIDENT_NOT_FOUND",
		"INCIDENT_REDIS_INDEX_FAILED", "INCIDENT_INDEX_COMPLETION_FAILED":
		return code
	default:
		return "unknown"
	}
}
