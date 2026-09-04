package observability

import (
	"GopherAI/internal/harness"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	requests                  *prometheus.CounterVec
	requestDuration           *prometheus.HistogramVec
	intentDecisions           *prometheus.CounterVec
	intentConfidence          *prometheus.HistogramVec
	intentShadowDecisions     *prometheus.CounterVec
	intentShadowDuration      *prometheus.HistogramVec
	intentShadowStageCalls    *prometheus.CounterVec
	intentShadowDisagreements *prometheus.CounterVec
	agentRuns                 *prometheus.CounterVec
	agentDuration             *prometheus.HistogramVec
	persistFailures           *prometheus.CounterVec
	documentUploads           *prometheus.CounterVec
	documentBytes             prometheus.Histogram
	retrievals                *prometheus.CounterVec
	retrievalDuration         *prometheus.HistogramVec
	retrievalResults          *prometheus.HistogramVec
	knowledgeAnswers          *prometheus.CounterVec
	knowledgeAnswerDuration   *prometheus.HistogramVec
	ragStrategyRequests       *prometheus.CounterVec
	ragStrategyDuration       *prometheus.HistogramVec
	ragEnhancements           *prometheus.CounterVec
	harnessRuns               *prometheus.CounterVec
	harnessTransitions        *prometheus.CounterVec
	harnessTerminals          *prometheus.CounterVec
	harnessDuration           *prometheus.HistogramVec
	harnessBudgetUtilization  *prometheus.HistogramVec
	gatherer                  prometheus.Gatherer
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
		intentShadowDecisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_intent_shadow_decisions_total",
			Help: "Total bounded shadow intent decisions; shadow decisions never change live routing.",
		}, []string{"intent", "final_stage", "status"}),
		intentShadowDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_intent_shadow_duration_seconds",
			Help:    "Shadow intent cascade duration in seconds by bounded final stage.",
			Buckets: []float64{0.0001, 0.001, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 4, 8},
		}, []string{"final_stage"}),
		intentShadowStageCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_intent_shadow_stage_calls_total",
			Help: "Total downstream shadow intent stage calls by bounded outcome.",
		}, []string{"stage", "outcome"}),
		intentShadowDisagreements: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_intent_shadow_disagreements_total",
			Help: "Total disagreements between the live route and the shadow intent suggestion.",
		}, []string{"legacy_route", "new_intent"}),
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
			Help: "Total KnowledgeAgent attempts by bounded outcome and evidence-gate reason.",
		}, []string{"status", "gate_reason"}),
		knowledgeAnswerDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_knowledge_answer_duration_seconds",
			Help:    "KnowledgeAgent rag_fast end-to-end duration in seconds.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30, 45},
		}, []string{"status"}),
		ragStrategyRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_rag_strategy_requests_total",
			Help: "Total RAG strategy requests by bounded strategy, outcome, and enhancement state.",
		}, []string{"strategy", "status", "enhancement"}),
		ragStrategyDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_rag_strategy_duration_seconds",
			Help:    "End-to-end RAG strategy duration in seconds.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30, 45, 60},
		}, []string{"strategy", "status"}),
		ragEnhancements: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_rag_enhancements_total",
			Help: "Total conditional RAG enhancement component outcomes.",
		}, []string{"component", "outcome"}),
		harnessRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_harness_runs_total",
			Help: "Total durable harness create requests by bounded intent, strategy and idempotency outcome.",
		}, []string{"intent", "strategy", "outcome"}),
		harnessTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_harness_transitions_total",
			Help: "Total successfully persisted harness state transitions; run identifiers are intentionally excluded.",
		}, []string{"intent", "strategy", "from_state", "to_state"}),
		harnessTerminals: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_harness_terminals_total",
			Help: "Total terminal harness outcomes by bounded state and reason.",
		}, []string{"intent", "strategy", "state", "reason"}),
		harnessDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_harness_duration_seconds",
			Help:    "Elapsed duration of terminal harness runs by bounded outcome.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 15, 30, 60, 300, 600},
		}, []string{"intent", "strategy", "state"}),
		harnessBudgetUtilization: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_harness_budget_utilization_ratio",
			Help:    "Terminal harness budget utilization ratio by bounded resource and outcome.",
			Buckets: []float64{0, 0.1, 0.25, 0.5, 0.75, 0.9, 1, 1.1},
		}, []string{"resource", "state"}),
		gatherer: gatherer,
	}
	registerer.MustRegister(
		metrics.requests,
		metrics.requestDuration,
		metrics.intentDecisions,
		metrics.intentConfidence,
		metrics.intentShadowDecisions,
		metrics.intentShadowDuration,
		metrics.intentShadowStageCalls,
		metrics.intentShadowDisagreements,
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
		metrics.ragStrategyRequests,
		metrics.ragStrategyDuration,
		metrics.ragEnhancements,
		metrics.harnessRuns,
		metrics.harnessTransitions,
		metrics.harnessTerminals,
		metrics.harnessDuration,
		metrics.harnessBudgetUtilization,
	)
	for _, status := range []string{"accepted", "duplicate", "rejected", "error"} {
		metrics.documentUploads.WithLabelValues(status).Add(0)
	}
	for _, intent := range []string{"project_qa", "troubleshooting", "doc_task", "tool_task", "follow_up", "general", "unknown"} {
		for _, stage := range []string{"pattern", "prototype", "llm", "degraded_clarification", "unavailable", "unknown"} {
			for _, status := range []string{"success", "degraded"} {
				metrics.intentShadowDecisions.WithLabelValues(intent, stage, status).Add(0)
			}
		}
	}
	for _, mode := range []string{"hybrid", "dense_only", "bm25_only", "unavailable"} {
		for _, status := range []string{"success", "degraded", "empty", "rejected", "error"} {
			metrics.retrievals.WithLabelValues(status, mode).Add(0)
		}
	}
	for _, status := range []string{"answered", "insufficient", "verifier_rejected", "rejected", "error"} {
		metrics.knowledgeAnswers.WithLabelValues(status, "none").Add(0)
	}
	for _, strategy := range []string{"rag_fast", "rag_deep"} {
		for _, status := range []string{"answered", "insufficient", "verifier_rejected", "rejected", "error"} {
			for _, enhancement := range []string{"skipped", "completed", "partial_fallback"} {
				metrics.ragStrategyRequests.WithLabelValues(strategy, status, enhancement).Add(0)
			}
		}
	}
	return metrics
}

// RecordRunCreate implements harness.Observer. Only fixed-domain values reach
// Prometheus labels; principal, request, trace and run identifiers are omitted.
func (metrics *Metrics) RecordRunCreate(run harness.Run, created bool) {
	if metrics == nil {
		return
	}
	outcome := "idempotent_replay"
	if created {
		outcome = "created"
	}
	metrics.harnessRuns.WithLabelValues(boundedHarnessIntent(run.Intent), boundedHarnessStrategy(run.Strategy), outcome).Inc()
}

// RecordRunTransition implements harness.Observer after a successful durable
// CAS transition. Terminal observations are emitted exactly once.
func (metrics *Metrics) RecordRunTransition(previous harness.Run, current harness.Run) {
	if metrics == nil {
		return
	}
	intent := boundedHarnessIntent(current.Intent)
	strategy := boundedHarnessStrategy(current.Strategy)
	fromState := boundedHarnessState(previous.State)
	toState := boundedHarnessState(current.State)
	metrics.harnessTransitions.WithLabelValues(intent, strategy, fromState, toState).Inc()
	if !harness.IsTerminal(current.State) {
		return
	}
	reason := boundedHarnessTerminalReason(current.TerminalReason)
	metrics.harnessTerminals.WithLabelValues(intent, strategy, toState, reason).Inc()
	duration := current.UpdatedAt.Sub(current.StartedAt)
	if current.FinishedAt != nil {
		duration = current.FinishedAt.Sub(current.StartedAt)
	}
	if duration < 0 {
		duration = 0
	}
	metrics.harnessDuration.WithLabelValues(intent, strategy, toState).Observe(duration.Seconds())
	recordBudgetRatio(metrics.harnessBudgetUtilization, "iterations", toState, current.Budget.UsedIterations, current.Budget.MaxIterations)
	recordBudgetRatio(metrics.harnessBudgetUtilization, "tool_calls", toState, current.Budget.UsedToolCalls, current.Budget.MaxToolCalls)
	recordBudgetRatio(metrics.harnessBudgetUtilization, "input_tokens", toState, current.Budget.UsedInputTokens, current.Budget.MaxInputTokens)
	recordBudgetRatio(metrics.harnessBudgetUtilization, "output_tokens", toState, current.Budget.UsedOutputTokens, current.Budget.MaxOutputTokens)
	if current.Budget.MaxCostMicros > 0 {
		metrics.harnessBudgetUtilization.WithLabelValues("cost", toState).Observe(float64(current.Budget.UsedCostMicros) / float64(current.Budget.MaxCostMicros))
	}
}

func recordBudgetRatio(metric *prometheus.HistogramVec, resource string, state string, used int, maximum int) {
	if maximum > 0 {
		metric.WithLabelValues(resource, state).Observe(float64(used) / float64(maximum))
	}
}

func boundedHarnessIntent(value string) string {
	if value == "troubleshooting" {
		return value
	}
	return "unknown"
}

func boundedHarnessStrategy(value string) string {
	if value == "diagnosis_standard" {
		return value
	}
	return "unknown"
}

func boundedHarnessState(value harness.State) string {
	switch value {
	case harness.StateReceived, harness.StateContextReady, harness.StatePlanned, harness.StateRunning,
		harness.StateWaitingUser, harness.StateSucceeded, harness.StateFailed, harness.StateCancelled,
		harness.StateBudgetExceeded:
		return string(value)
	default:
		return "UNKNOWN"
	}
}

func boundedHarnessTerminalReason(value string) string {
	switch value {
	case "DIAGNOSTIC_HYPOTHESES_READY", "USER_CANCELLED", "TIME_BUDGET_EXCEEDED", "EXECUTION_BUDGET_EXCEEDED", "NO_PROGRESS":
		return value
	case "":
		return "NONE"
	default:
		return "OTHER"
	}
}

func (metrics *Metrics) RecordRAGStrategy(strategy string, status string, enhancement string, duration time.Duration, rewriteOutcome string, rerankOutcome string) {
	if metrics == nil {
		return
	}
	strategy = boundedRAGStrategy(strategy)
	status = boundedRAGStatus(status)
	enhancement = boundedEnhancement(enhancement)
	metrics.ragStrategyRequests.WithLabelValues(strategy, status, enhancement).Inc()
	metrics.ragStrategyDuration.WithLabelValues(strategy, status).Observe(duration.Seconds())
	if rewriteOutcome != "" && rewriteOutcome != "none" {
		metrics.ragEnhancements.WithLabelValues("rewrite", boundedEnhancementOutcome(rewriteOutcome)).Inc()
	}
	if rerankOutcome != "" && rerankOutcome != "none" {
		metrics.ragEnhancements.WithLabelValues("rerank", boundedEnhancementOutcome(rerankOutcome)).Inc()
	}
}

func boundedRAGStrategy(strategy string) string {
	if strategy == "rag_deep" {
		return strategy
	}
	return "rag_fast"
}

func boundedRAGStatus(status string) string {
	switch status {
	case "answered", "insufficient", "verifier_rejected", "rejected", "error":
		return status
	default:
		return "error"
	}
}

func boundedEnhancement(enhancement string) string {
	switch enhancement {
	case "skipped", "completed", "partial_fallback":
		return enhancement
	default:
		return "partial_fallback"
	}
}

func boundedEnhancementOutcome(outcome string) string {
	switch outcome {
	case "rewrite_not_required", "rewrite_completed", "rewrite_model_error", "rewrite_timeout", "rewrite_invalid_output",
		"rerank_not_required", "rerank_completed", "rerank_model_error", "rerank_timeout", "rerank_invalid_output":
		return outcome
	default:
		return "unknown"
	}
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
