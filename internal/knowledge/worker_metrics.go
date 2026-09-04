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
	registerer.MustRegister(metrics.outboxPublishes, metrics.outboxOldestAge, metrics.indexJobs, metrics.indexDuration, metrics.incidentJobs, metrics.incidentDuration)
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
	if code == "" {
		code = "none"
	}
	metrics.incidentJobs.WithLabelValues(status, code).Inc()
	metrics.incidentDuration.Observe(duration.Seconds())
}

func (metrics *WorkerMetrics) RecordOutboxPublish(status string) {
	metrics.outboxPublishes.WithLabelValues(status).Inc()
}

func (metrics *WorkerMetrics) SetOutboxOldestAge(seconds float64) {
	metrics.outboxOldestAge.Set(seconds)
}

func (metrics *WorkerMetrics) RecordIndexJob(status string, code string, duration time.Duration) {
	if code == "" {
		code = "none"
	}
	metrics.indexJobs.WithLabelValues(status, code).Inc()
	metrics.indexDuration.Observe(duration.Seconds())
}
