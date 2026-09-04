package observability

import (
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	requests                *prometheus.CounterVec
	requestDuration         *prometheus.HistogramVec
	intentDecisions         *prometheus.CounterVec
	intentConfidence        *prometheus.HistogramVec
	agentRuns               *prometheus.CounterVec
	agentDuration           *prometheus.HistogramVec
	persistFailures         *prometheus.CounterVec
	documentUploads         *prometheus.CounterVec
	documentBytes           prometheus.Histogram
	retrievals              *prometheus.CounterVec
	retrievalDuration       *prometheus.HistogramVec
	retrievalResults        *prometheus.HistogramVec
	knowledgeAnswers        *prometheus.CounterVec
	knowledgeAnswerDuration *prometheus.HistogramVec
	gatherer                prometheus.Gatherer
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
		documentUploads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_document_uploads_total",
			Help: "Total knowledge document upload attempts by bounded outcome.",
		}, []string{"status"}),
		documentBytes: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "gopherai_document_upload_bytes",
			Help:    "Accepted knowledge document size in bytes.",
			Buckets: []float64{1024, 10 * 1024, 100 * 1024, 1024 * 1024, 5 * 1024 * 1024, 10 * 1024 * 1024},
		}),
		retrievals: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_knowledge_retrievals_total",
			Help: "Total knowledge retrieval attempts by bounded outcome and retrieval mode.",
		}, []string{"status", "mode"}),
		retrievalDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_knowledge_retrieval_duration_seconds",
			Help:    "Knowledge hybrid retrieval duration in seconds.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30, 45},
		}, []string{"mode"}),
		retrievalResults: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_knowledge_retrieval_results",
			Help:    "Number of authoritative knowledge chunks returned per retrieval.",
			Buckets: []float64{0, 1, 2, 3, 5, 8, 10},
		}, []string{"mode"}),
		knowledgeAnswers: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_knowledge_answers_total",
			Help: "Total KnowledgeAgent rag_fast attempts by bounded outcome and evidence-gate reason.",
		}, []string{"status", "gate_reason"}),
		knowledgeAnswerDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_knowledge_answer_duration_seconds",
			Help:    "KnowledgeAgent rag_fast end-to-end duration in seconds.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30, 45},
		}, []string{"status"}),
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
		metrics.documentUploads,
		metrics.documentBytes,
		metrics.retrievals,
		metrics.retrievalDuration,
		metrics.retrievalResults,
		metrics.knowledgeAnswers,
		metrics.knowledgeAnswerDuration,
	)
	for _, status := range []string{"accepted", "duplicate", "rejected", "error"} {
		metrics.documentUploads.WithLabelValues(status).Add(0)
	}
	for _, mode := range []string{"hybrid", "dense_only", "bm25_only", "unavailable"} {
		for _, status := range []string{"success", "degraded", "empty", "rejected", "error"} {
			metrics.retrievals.WithLabelValues(status, mode).Add(0)
		}
	}
	for _, status := range []string{"answered", "insufficient", "verifier_rejected", "rejected", "error"} {
		metrics.knowledgeAnswers.WithLabelValues(status, "none").Add(0)
	}
	return metrics
}

func (metrics *Metrics) RecordKnowledgeAnswer(status string, gateReason string, duration time.Duration) {
	if metrics == nil {
		return
	}
	switch status {
	case "answered", "insufficient", "verifier_rejected", "rejected", "error":
	default:
		status = "error"
	}
	switch gateReason {
	case "sufficient", "no_evidence", "no_cross_retriever_support", "low_top_score":
	default:
		gateReason = "none"
	}
	metrics.knowledgeAnswers.WithLabelValues(status, gateReason).Inc()
	metrics.knowledgeAnswerDuration.WithLabelValues(status).Observe(duration.Seconds())
}

func (metrics *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(metrics.gatherer, promhttp.HandlerOpts{})
}

func (metrics *Metrics) RecordDocumentUpload(status string, sizeBytes int64) {
	metrics.documentUploads.WithLabelValues(status).Inc()
	if status == "accepted" && sizeBytes >= 0 {
		metrics.documentBytes.Observe(float64(sizeBytes))
	}
}

func (metrics *Metrics) RecordKnowledgeRetrieval(status string, mode string, duration time.Duration, resultCount int) {
	if metrics == nil {
		return
	}
	status = boundedRetrievalStatus(status)
	mode = boundedRetrievalMode(mode)
	metrics.retrievals.WithLabelValues(status, mode).Inc()
	metrics.retrievalDuration.WithLabelValues(mode).Observe(duration.Seconds())
	if resultCount < 0 {
		resultCount = 0
	}
	metrics.retrievalResults.WithLabelValues(mode).Observe(float64(resultCount))
}

func boundedRetrievalStatus(status string) string {
	switch status {
	case "success", "degraded", "empty", "rejected", "error":
		return status
	default:
		return "error"
	}
}

func boundedRetrievalMode(mode string) string {
	switch mode {
	case "hybrid", "dense_only", "bm25_only", "unavailable":
		return mode
	default:
		return "unavailable"
	}
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
