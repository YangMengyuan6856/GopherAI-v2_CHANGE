package observability

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	requests         *prometheus.CounterVec
	requestDuration  *prometheus.HistogramVec
	intentDecisions  *prometheus.CounterVec
	intentConfidence *prometheus.HistogramVec
	agentRuns        *prometheus.CounterVec
	agentDuration    *prometheus.HistogramVec
	persistFailures  *prometheus.CounterVec
	gatherer         prometheus.Gatherer
}

func NewMetrics(registerer prometheus.Registerer, gatherer prometheus.Gatherer) *Metrics {
	metrics := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_requests_total",
			Help: "Total AppService requests by bounded routing outcome.",
		}, []string{"intent", "strategy", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_request_duration_seconds",
			Help:    "End-to-end AppService request duration in seconds.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30, 60, 90},
		}, []string{"intent", "strategy"}),
		intentDecisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_intent_decisions_total",
			Help: "Total intent decisions by bounded intent and final stage.",
		}, []string{"intent", "final_stage", "status"}),
		intentConfidence: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_intent_confidence",
			Help:    "Intent confidence distribution.",
			Buckets: prometheus.LinearBuckets(0, 0.1, 11),
		}, []string{"intent", "final_stage"}),
		agentRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_agent_runs_total",
			Help: "Total agent strategy executions.",
		}, []string{"agent", "strategy", "status"}),
		agentDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_agent_duration_seconds",
			Help:    "Agent strategy execution duration in seconds.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30, 60, 90},
		}, []string{"agent", "strategy"}),
		persistFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_observability_persist_failures_total",
			Help: "Total sanitized observability persistence failures.",
		}, []string{"record_type"}),
		gatherer: gatherer,
	}
	registerer.MustRegister(
		metrics.requests,
		metrics.requestDuration,
		metrics.intentDecisions,
		metrics.intentConfidence,
		metrics.agentRuns,
		metrics.agentDuration,
		metrics.persistFailures,
	)
	return metrics
}

func (metrics *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(metrics.gatherer, promhttp.HandlerOpts{})
}

var (
	defaultMetricsOnce sync.Once
	defaultMetrics     *Metrics
)

func DefaultMetrics() *Metrics {
	defaultMetricsOnce.Do(func() {
		defaultMetrics = NewMetrics(prometheus.DefaultRegisterer, prometheus.DefaultGatherer)
	})
	return defaultMetrics
}

func MetricsHandler() http.Handler { return DefaultMetrics().Handler() }
