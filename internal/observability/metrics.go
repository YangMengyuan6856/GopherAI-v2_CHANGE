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
	requests                    *prometheus.CounterVec
	requestDuration             *prometheus.HistogramVec
	ttft                        *prometheus.HistogramVec
	modelCalls                  *prometheus.CounterVec
	modelTokens                 *prometheus.CounterVec
	modelCostMicros             *prometheus.CounterVec
	intentDecisions             *prometheus.CounterVec
	intentConfidence            *prometheus.HistogramVec
	intentStageDuration         *prometheus.HistogramVec
	intentClarifications        *prometheus.CounterVec
	intentShadowDecisions       *prometheus.CounterVec
	intentShadowDuration        *prometheus.HistogramVec
	intentShadowStageCalls      *prometheus.CounterVec
	intentShadowDisagreements   *prometheus.CounterVec
	agentRuns                   *prometheus.CounterVec
	agentDuration               *prometheus.HistogramVec
	persistFailures             *prometheus.CounterVec
	documentUploads             *prometheus.CounterVec
	documentBytes               prometheus.Histogram
	retrievals                  *prometheus.CounterVec
	retrievalDuration           *prometheus.HistogramVec
	retrievalResults            *prometheus.HistogramVec
	knowledgeAnswers            *prometheus.CounterVec
	knowledgeAnswerDuration     *prometheus.HistogramVec
	ragStrategyRequests         *prometheus.CounterVec
	ragStrategyDuration         *prometheus.HistogramVec
	ragEnhancements             *prometheus.CounterVec
	ragQueries                  *prometheus.CounterVec
	ragDuration                 *prometheus.HistogramVec
	ragCandidates               *prometheus.HistogramVec
	ragTopScore                 *prometheus.HistogramVec
	ragEmpty                    *prometheus.CounterVec
	ragRewrite                  *prometheus.CounterVec
	ragRerank                   *prometheus.CounterVec
	citations                   *prometheus.CounterVec
	caseMemoryRecalls           *prometheus.CounterVec
	caseMemoryRecallDuration    *prometheus.HistogramVec
	caseMemoryRecallResults     *prometheus.HistogramVec
	caseStrategyRuns            *prometheus.CounterVec
	caseStrategyDuration        *prometheus.HistogramVec
	collaborationPlans          *prometheus.CounterVec
	collaborationPlanDuration   *prometheus.HistogramVec
	collaborationRuns           *prometheus.CounterVec
	collaborationRunDuration    *prometheus.HistogramVec
	collaborationTasks          *prometheus.CounterVec
	collaborationTaskDuration   *prometheus.HistogramVec
	collaborationSyntheses      *prometheus.CounterVec
	profileMemoryRecalls        *prometheus.CounterVec
	profileMemoryRecallDuration *prometheus.HistogramVec
	profileMemoryRecallResults  *prometheus.HistogramVec
	harnessRuns                 *prometheus.CounterVec
	harnessTransitions          *prometheus.CounterVec
	harnessTerminals            *prometheus.CounterVec
	harnessDuration             *prometheus.HistogramVec
	harnessBudgetUtilization    *prometheus.HistogramVec
	agentBudgetExceeded         *prometheus.CounterVec
	agentRunTransitions         *prometheus.CounterVec
	agentResume                 *prometheus.CounterVec
	agentNoProgress             *prometheus.CounterVec
	agentActiveRuns             *prometheus.GaugeVec
	contextTokens               *prometheus.HistogramVec
	contextRetentionChecks      *prometheus.CounterVec
	memoryRecall                *prometheus.CounterVec
	memoryCandidates            *prometheus.HistogramVec
	toolCalls                   *prometheus.CounterVec
	toolDuration                *prometheus.HistogramVec
	toolRetries                 *prometheus.CounterVec
	toolCircuitState            *prometheus.GaugeVec
	toolCache                   *prometheus.CounterVec
	toolValidation              *prometheus.CounterVec
	toolCancellations           *prometheus.CounterVec
	legacyEntryAttempts         *prometheus.CounterVec
	policyLoads                 *prometheus.CounterVec
	strategyWeights             *prometheus.GaugeVec
	feedback                    *prometheus.CounterVec
	onlineEvalScore             *prometheus.HistogramVec
	onlineEvalFailures          *prometheus.CounterVec
	evalRegressions             *prometheus.CounterVec
	strategyState               *prometheus.GaugeVec
	controlActions              *prometheus.CounterVec
	controlLoopDuration         *prometheus.HistogramVec
	webhookDeliveries           *prometheus.CounterVec
	gatherer                    prometheus.Gatherer
}

// Collectors returns the complete set of application-owned Prometheus
// collectors. Registration and catalog discovery share this list so the
// published metric contract cannot silently drift from the runtime registry.
func (metrics *Metrics) Collectors() []prometheus.Collector {
	if metrics == nil {
		return nil
	}
	return []prometheus.Collector{
		metrics.requests, metrics.requestDuration, metrics.ttft,
		metrics.modelCalls, metrics.modelTokens, metrics.modelCostMicros,
		metrics.intentDecisions, metrics.intentConfidence, metrics.intentStageDuration, metrics.intentClarifications,
		metrics.intentShadowDecisions, metrics.intentShadowDuration,
		metrics.intentShadowStageCalls, metrics.intentShadowDisagreements,
		metrics.agentRuns, metrics.agentDuration, metrics.persistFailures,
		metrics.documentUploads, metrics.documentBytes,
		metrics.retrievals, metrics.retrievalDuration, metrics.retrievalResults,
		metrics.knowledgeAnswers, metrics.knowledgeAnswerDuration,
		metrics.ragStrategyRequests, metrics.ragStrategyDuration, metrics.ragEnhancements,
		metrics.ragQueries, metrics.ragDuration, metrics.ragCandidates, metrics.ragTopScore,
		metrics.ragEmpty, metrics.ragRewrite, metrics.ragRerank, metrics.citations,
		metrics.caseMemoryRecalls, metrics.caseMemoryRecallDuration, metrics.caseMemoryRecallResults,
		metrics.caseStrategyRuns, metrics.caseStrategyDuration,
		metrics.collaborationPlans, metrics.collaborationPlanDuration,
		metrics.collaborationRuns, metrics.collaborationRunDuration,
		metrics.collaborationTasks, metrics.collaborationTaskDuration, metrics.collaborationSyntheses,
		metrics.profileMemoryRecalls, metrics.profileMemoryRecallDuration, metrics.profileMemoryRecallResults,
		metrics.harnessRuns, metrics.harnessTransitions, metrics.harnessTerminals,
		metrics.harnessDuration, metrics.harnessBudgetUtilization,
		metrics.agentBudgetExceeded, metrics.agentRunTransitions, metrics.agentResume, metrics.agentNoProgress,
		metrics.agentActiveRuns, metrics.contextTokens, metrics.contextRetentionChecks,
		metrics.memoryRecall, metrics.memoryCandidates,
		metrics.toolCalls, metrics.toolDuration, metrics.toolRetries, metrics.toolCircuitState,
		metrics.toolCache, metrics.toolValidation, metrics.toolCancellations,
		metrics.legacyEntryAttempts, metrics.policyLoads, metrics.strategyWeights,
		metrics.feedback, metrics.onlineEvalScore, metrics.onlineEvalFailures, metrics.evalRegressions,
		metrics.strategyState, metrics.controlActions, metrics.controlLoopDuration, metrics.webhookDeliveries,
	}
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
		ttft: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_ttft_seconds",
			Help:    "Time to first generated token in seconds by bounded strategy.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30},
		}, []string{"strategy"}),
		modelCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_model_calls_total",
			Help: "Total model calls by bounded purpose, model alias and outcome.",
		}, []string{"purpose", "model", "status"}),
		modelTokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_model_tokens_total",
			Help: "Total estimated model tokens by bounded purpose, model alias and direction.",
		}, []string{"purpose", "model", "direction"}),
		modelCostMicros: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_model_cost_micros_total",
			Help: "Total estimated model cost in micro-units by bounded purpose and model alias.",
		}, []string{"purpose", "model"}),
		intentDecisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_intent_decisions_total",
			Help: "Total intent decisions by bounded intent and final stage.",
		}, []string{"intent", "final_stage", "status"}),
		intentConfidence: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_intent_confidence",
			Help:    "Intent confidence distribution.",
			Buckets: prometheus.LinearBuckets(0, 0.1, 11),
		}, []string{"intent", "final_stage"}),
		intentStageDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_intent_stage_duration_seconds",
			Help:    "Intent cascade stage duration in seconds by bounded stage.",
			Buckets: []float64{0.0001, 0.001, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 4, 8},
		}, []string{"stage"}),
		intentClarifications: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_intent_clarifications_total",
			Help: "Total clarification decisions by bounded predicted intent.",
		}, []string{"predicted_intent"}),
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
		ragQueries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_rag_queries_total",
			Help: "Total RAG queries by bounded strategy and outcome.",
		}, []string{"strategy", "status"}),
		ragDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_rag_duration_seconds",
			Help:    "RAG stage duration in seconds by bounded stage and strategy.",
			Buckets: []float64{0.001, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30, 60},
		}, []string{"stage", "strategy"}),
		ragCandidates: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_rag_candidates",
			Help:    "RAG candidate count by bounded retrieval source.",
			Buckets: []float64{0, 1, 2, 3, 5, 8, 10, 20, 40},
		}, []string{"source"}),
		ragTopScore: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_rag_top_score",
			Help:    "Highest normalized retrieval or rerank score by bounded strategy.",
			Buckets: prometheus.LinearBuckets(0, 0.1, 11),
		}, []string{"strategy"}),
		ragEmpty: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_rag_empty_total",
			Help: "Total empty or evidence-gated RAG results by bounded reason.",
		}, []string{"strategy", "reason"}),
		ragRewrite: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_rag_rewrite_total",
			Help: "Total bounded query rewrite outcomes.",
		}, []string{"result"}),
		ragRerank: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_rag_rerank_total",
			Help: "Total bounded rerank outcomes.",
		}, []string{"result"}),
		citations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_citations_total",
			Help: "Total citation verification outcomes.",
		}, []string{"verification"}),
		caseMemoryRecalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_case_memory_recalls_total",
			Help: "Total advisory episodic-memory recalls by bounded outcome.",
		}, []string{"status"}),
		caseMemoryRecallDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_case_memory_recall_duration_seconds",
			Help:    "Episodic-memory recall duration in seconds by bounded outcome.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		}, []string{"status"}),
		caseMemoryRecallResults: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_case_memory_recall_results",
			Help:    "Number of user-confirmed historical cases returned by recall.",
			Buckets: []float64{0, 1, 2, 3},
		}, []string{"status"}),
		caseStrategyRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_case_strategy_runs_total",
			Help: "Total diagnosis_case_based shadow runs by bounded match strength and outcome.",
		}, []string{"strength", "outcome"}),
		caseStrategyDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_case_strategy_duration_seconds",
			Help:    "End-to-end diagnosis_case_based shadow duration in seconds.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2},
		}, []string{"outcome"}),
		collaborationPlans: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_collaboration_plans_total",
			Help: "Total bounded collaboration shadow plans by decision and reason.",
		}, []string{"decision", "reason"}),
		collaborationPlanDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_collaboration_plan_duration_seconds",
			Help:    "Bounded collaboration planner duration in seconds by decision.",
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1},
		}, []string{"decision"}),
		collaborationRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_collaboration_runs_total",
			Help: "Total diagnosis_collaborative shadow runs by bounded planner decision and outcome.",
		}, []string{"decision", "status"}),
		collaborationRunDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_collaboration_run_duration_seconds",
			Help:    "End-to-end bounded collaboration shadow duration in seconds.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30, 45, 60, 90, 120},
		}, []string{"decision", "status"}),
		collaborationTasks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_collaboration_agent_tasks_total",
			Help: "Total bounded collaboration child tasks by fixed agent role and status.",
		}, []string{"agent", "status"}),
		collaborationTaskDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_collaboration_agent_task_duration_seconds",
			Help:    "Bounded collaboration child-task duration by fixed agent role and status.",
			Buckets: []float64{0.001, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30, 45, 60},
		}, []string{"agent", "status"}),
		collaborationSyntheses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_collaboration_syntheses_total",
			Help: "Total evidence-aware synthesis outcomes with bounded reason labels.",
		}, []string{"status", "reason"}),
		profileMemoryRecalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_profile_memory_recalls_total",
			Help: "Total governed profile-memory recalls by bounded outcome.",
		}, []string{"status"}),
		profileMemoryRecallDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_profile_memory_recall_duration_seconds",
			Help:    "Profile-memory recall duration in seconds by bounded outcome.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		}, []string{"status"}),
		profileMemoryRecallResults: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_profile_memory_recall_results",
			Help:    "Number of active relevant profile facts returned per recall.",
			Buckets: []float64{0, 1, 2, 3, 5},
		}, []string{"status"}),
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
		agentBudgetExceeded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_agent_budget_exceeded_total",
			Help: "Total Agent budget breaches by bounded resource type.",
		}, []string{"budget_type"}),
		agentRunTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_agent_run_transitions_total",
			Help: "Total Agent Run state transition attempts by bounded states and result.",
		}, []string{"from", "to", "result"}),
		agentResume: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_agent_resume_total",
			Help: "Total Agent checkpoint resume attempts by bounded result.",
		}, []string{"result"}),
		agentNoProgress: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_agent_no_progress_total",
			Help: "Total repeated Agent actions blocked by the no-progress guard.",
		}, []string{"agent", "strategy"}),
		agentActiveRuns: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gopherai_agent_active_runs",
			Help: "Current durable Agent Runs by bounded state.",
		}, []string{"state"}),
		contextTokens: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_context_tokens",
			Help:    "Estimated Context Assembler tokens by bounded segment and direction.",
			Buckets: []float64{0, 128, 256, 512, 1024, 2048, 4096, 8192, 16384},
		}, []string{"segment", "direction"}),
		contextRetentionChecks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_context_retention_checks_total",
			Help: "Total Context Assembler invariant checks by bounded field and result.",
		}, []string{"field", "result"}),
		memoryRecall: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_memory_recall_total",
			Help: "Total three-tier memory recall outcomes by bounded tier.",
		}, []string{"tier", "status"}),
		memoryCandidates: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_memory_candidates",
			Help:    "Memory candidates considered by bounded memory tier.",
			Buckets: []float64{0, 1, 2, 3, 5, 8, 10, 20, 50, 100},
		}, []string{"tier"}),
		toolCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_tool_calls_total",
			Help: "Total governed tool calls by bounded tool, strategy and terminal status.",
		}, []string{"tool", "strategy", "status"}),
		toolDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_tool_duration_seconds",
			Help:    "Governed tool execution duration in seconds.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 15, 30},
		}, []string{"tool", "strategy"}),
		toolRetries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_tool_retries_total",
			Help: "Total governed tool retries by bounded reason.",
		}, []string{"tool", "reason"}),
		toolCircuitState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gopherai_tool_circuit_state",
			Help: "Current governed tool circuit state represented as a one-hot gauge.",
		}, []string{"tool", "state"}),
		toolCache: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_tool_cache_total",
			Help: "Total governed tool cache outcomes.",
		}, []string{"tool", "result"}),
		toolValidation: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_tool_validation_total",
			Help: "Total governed tool registry, schema and authorization validation outcomes.",
		}, []string{"tool", "result"}),
		toolCancellations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_tool_cancellations_total",
			Help: "Total governed tool cancellations by bounded reason.",
		}, []string{"tool", "reason"}),
		legacyEntryAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_legacy_entry_attempts_total",
			Help: "Total requests to retired public entry points; only fixed route classes are exposed.",
		}, []string{"entry"}),
		policyLoads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_policy_loads_total",
			Help: "Total active routing policy loads by bounded source and outcome.",
		}, []string{"source", "result"}),
		strategyWeights: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gopherai_strategy_weight",
			Help: "Configured strategy weight in basis points for the bounded active policy version.",
		}, []string{"intent", "strategy", "policy_version_short"}),
		feedback: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_feedback_total",
			Help: "Total explicit and implicit feedback outcomes by bounded strategy and feedback type.",
		}, []string{"strategy", "type", "result"}),
		onlineEvalScore: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_online_eval_score",
			Help:    "Online evaluation score by bounded strategy and quality dimension.",
			Buckets: prometheus.LinearBuckets(0, 0.1, 11),
		}, []string{"strategy", "dimension"}),
		onlineEvalFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_online_eval_failures_total",
			Help: "Total online evaluation failures by bounded strategy and reason.",
		}, []string{"strategy", "reason"}),
		evalRegressions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_eval_regressions_total",
			Help: "Total detected evaluation regressions by fixed suite and metric.",
		}, []string{"suite", "metric"}),
		strategyState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gopherai_strategy_state",
			Help: "Current bounded strategy health state represented as a one-hot gauge.",
		}, []string{"intent", "strategy", "state"}),
		controlActions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_control_actions_total",
			Help: "Total control-loop recommendations by bounded action and result.",
		}, []string{"action", "result"}),
		controlLoopDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gopherai_control_loop_duration_seconds",
			Help:    "Control-loop evaluation duration in seconds by bounded result.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
		}, []string{"result"}),
		webhookDeliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gopherai_webhook_deliveries_total",
			Help: "Total control-plane webhook delivery outcomes by bounded event type.",
		}, []string{"event_type", "status"}),
		gatherer: gatherer,
	}
	registerer.MustRegister(metrics.Collectors()...)
	metrics.initializeRequiredMetricSeries()
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
	for _, status := range []string{"hit", "no_match", "unavailable"} {
		metrics.caseMemoryRecalls.WithLabelValues(status).Add(0)
		metrics.profileMemoryRecalls.WithLabelValues(status).Add(0)
	}
	for _, strength := range []string{"strong", "weak", "none"} {
		for _, outcome := range []string{"success", "fallback", "error", "cancelled"} {
			metrics.caseStrategyRuns.WithLabelValues(strength, outcome).Add(0)
		}
	}
	for _, decision := range []string{"single_agent", "collaborative_candidate", "error", "cancelled"} {
		for _, reason := range []string{"single_task_preferred", "independent_diagnostic_branches", "knowledge_diagnostic_split", "conflict_requires_evidence_verification", "error"} {
			metrics.collaborationPlans.WithLabelValues(decision, reason).Add(0)
		}
	}
	for _, decision := range []string{"single_agent", "collaborative_candidate", "error"} {
		for _, status := range []string{"not_executed", "complete", "partial", "conflict", "insufficient", "failed", "cancelled"} {
			metrics.collaborationRuns.WithLabelValues(decision, status).Add(0)
		}
	}
	for _, agent := range []string{"KnowledgeAgent", "DiagnosticAgent", "error"} {
		for _, status := range []string{"succeeded", "failed", "timed_out", "cancelled", "budget_exceeded", "insufficient"} {
			metrics.collaborationTasks.WithLabelValues(agent, status).Add(0)
		}
	}
	for _, status := range []string{"complete", "partial", "conflict", "insufficient", "error"} {
		for _, reason := range []string{"all_claims_citation_verified", "partial_agent_results", "evidence_conflict_requires_user_review", "no_supported_claims", "error"} {
			metrics.collaborationSyntheses.WithLabelValues(status, reason).Add(0)
		}
	}
	for _, strategy := range []string{"rag_fast", "rag_deep"} {
		for _, status := range []string{"answered", "insufficient", "verifier_rejected", "rejected", "error"} {
			for _, enhancement := range []string{"skipped", "completed", "partial_fallback"} {
				metrics.ragStrategyRequests.WithLabelValues(strategy, status, enhancement).Add(0)
			}
		}
	}
	for _, tool := range []string{"deployment_manifest_lookup", "service_health_snapshot", "bounded_log_signature", "mcp_deployment_evidence", "official_document_search", "confirm_resolution"} {
		for _, state := range []string{"closed", "open", "half_open"} {
			value := 0.0
			if state == "closed" {
				value = 1
			}
			metrics.toolCircuitState.WithLabelValues(tool, state).Set(value)
		}
		for _, result := range []string{"hit", "miss", "stale_fallback", "bypass", "store_error"} {
			metrics.toolCache.WithLabelValues(tool, result).Add(0)
		}
	}
	metrics.legacyEntryAttempts.WithLabelValues("skill_api").Add(0)
	for _, source := range []string{"redis", "mysql"} {
		for _, result := range []string{"success", "cache_degraded", "invalid", "error"} {
			metrics.policyLoads.WithLabelValues(source, result).Add(0)
		}
	}
	return metrics
}

// initializeRequiredMetricSeries exposes every SDD-required metric family on
// /metrics before the first production event. Only fixed fallback labels are
// used, so startup visibility cannot introduce caller-controlled cardinality.
func (metrics *Metrics) initializeRequiredMetricSeries() {
	metrics.requests.WithLabelValues("unknown", "unknown", "error").Add(0)
	metrics.requestDuration.WithLabelValues("unknown", "unknown")
	metrics.ttft.WithLabelValues("unknown")
	metrics.modelCalls.WithLabelValues("other", "other", "error").Add(0)
	metrics.modelTokens.WithLabelValues("other", "other", "input").Add(0)
	metrics.modelCostMicros.WithLabelValues("other", "other").Add(0)
	metrics.intentStageDuration.WithLabelValues("unknown")
	metrics.intentClarifications.WithLabelValues("unknown").Add(0)
	metrics.intentDecisions.WithLabelValues("unknown", "unknown", "degraded").Add(0)
	metrics.intentConfidence.WithLabelValues("unknown", "unknown")
	metrics.intentShadowDisagreements.WithLabelValues("unknown", "unknown").Add(0)
	metrics.ragQueries.WithLabelValues("rag_fast", "error").Add(0)
	metrics.ragDuration.WithLabelValues("end_to_end", "rag_fast")
	metrics.ragCandidates.WithLabelValues("fused")
	metrics.ragTopScore.WithLabelValues("rag_fast")
	metrics.ragEmpty.WithLabelValues("rag_fast", "error").Add(0)
	metrics.ragRewrite.WithLabelValues("skipped").Add(0)
	metrics.ragRerank.WithLabelValues("skipped").Add(0)
	metrics.citations.WithLabelValues("rejected").Add(0)
	metrics.agentRuns.WithLabelValues("unknown", "unknown", "error").Add(0)
	metrics.agentDuration.WithLabelValues("unknown", "unknown")
	metrics.agentBudgetExceeded.WithLabelValues("token").Add(0)
	metrics.agentRunTransitions.WithLabelValues("new", "new", "success").Add(0)
	metrics.agentResume.WithLabelValues("no_checkpoint").Add(0)
	metrics.agentNoProgress.WithLabelValues("DiagnosticAgent", "diagnosis_standard").Add(0)
	metrics.agentActiveRuns.WithLabelValues("new").Set(0)
	metrics.contextTokens.WithLabelValues("working", "input")
	metrics.contextRetentionChecks.WithLabelValues("goal", "passed").Add(0)
	metrics.memoryRecall.WithLabelValues("working", "no_match").Add(0)
	metrics.memoryCandidates.WithLabelValues("working")
	metrics.toolCalls.WithLabelValues("unknown", "unknown", "error").Add(0)
	metrics.toolDuration.WithLabelValues("unknown", "unknown")
	metrics.toolRetries.WithLabelValues("unknown", "unknown").Add(0)
	metrics.toolCircuitState.WithLabelValues("unknown", "open").Set(0)
	metrics.toolCache.WithLabelValues("unknown", "bypass").Add(0)
	metrics.toolValidation.WithLabelValues("unknown", "unknown").Add(0)
	metrics.toolCancellations.WithLabelValues("unknown", "unknown").Add(0)
	metrics.feedback.WithLabelValues("legacy_chat", "explicit", "accepted").Add(0)
	metrics.onlineEvalScore.WithLabelValues("legacy_chat", "quality")
	metrics.onlineEvalFailures.WithLabelValues("legacy_chat", "error").Add(0)
	metrics.evalRegressions.WithLabelValues("unified", "completion").Add(0)
	metrics.strategyWeights.WithLabelValues("unknown", "unknown", "other").Set(0)
	metrics.strategyState.WithLabelValues("unknown", "unknown", "healthy").Set(0)
	metrics.controlActions.WithLabelValues("none", "suppressed").Add(0)
	metrics.controlLoopDuration.WithLabelValues("success")
	metrics.webhookDeliveries.WithLabelValues("anomaly", "queued").Add(0)
}

func (metrics *Metrics) RecordCaseStrategy(strength string, outcome string, duration time.Duration) {
	if metrics == nil {
		return
	}
	switch strength {
	case "strong", "weak", "none":
	default:
		strength = "none"
	}
	switch outcome {
	case "success", "fallback", "error", "cancelled":
	default:
		outcome = "error"
	}
	if duration < 0 {
		duration = 0
	}
	metrics.caseStrategyRuns.WithLabelValues(strength, outcome).Inc()
	metrics.caseStrategyDuration.WithLabelValues(outcome).Observe(duration.Seconds())
}

func (metrics *Metrics) RecordCollaborationPlan(decision string, reason string, duration time.Duration) {
	if metrics == nil {
		return
	}
	switch decision {
	case "single_agent", "collaborative_candidate", "error", "cancelled":
	default:
		decision = "error"
	}
	switch reason {
	case "single_task_preferred", "independent_diagnostic_branches", "knowledge_diagnostic_split", "conflict_requires_evidence_verification":
	default:
		reason = "error"
	}
	if duration < 0 {
		duration = 0
	}
	metrics.collaborationPlans.WithLabelValues(decision, reason).Inc()
	metrics.collaborationPlanDuration.WithLabelValues(decision).Observe(duration.Seconds())
}

func (metrics *Metrics) RecordCollaborationRun(decision string, status string, duration time.Duration) {
	if metrics == nil {
		return
	}
	switch decision {
	case "single_agent", "collaborative_candidate":
	default:
		decision = "error"
	}
	switch status {
	case "not_executed", "complete", "partial", "conflict", "insufficient", "failed", "cancelled":
	default:
		status = "failed"
	}
	if duration < 0 {
		duration = 0
	}
	metrics.collaborationRuns.WithLabelValues(decision, status).Inc()
	metrics.collaborationRunDuration.WithLabelValues(decision, status).Observe(duration.Seconds())
}

func (metrics *Metrics) RecordCollaborationTask(agent string, status string, duration time.Duration) {
	if metrics == nil {
		return
	}
	if agent != "KnowledgeAgent" && agent != "DiagnosticAgent" {
		agent = "error"
	}
	switch status {
	case "succeeded", "failed", "timed_out", "cancelled", "budget_exceeded", "insufficient":
	default:
		status = "failed"
	}
	if duration < 0 {
		duration = 0
	}
	metrics.collaborationTasks.WithLabelValues(agent, status).Inc()
	metrics.collaborationTaskDuration.WithLabelValues(agent, status).Observe(duration.Seconds())
}

func (metrics *Metrics) RecordCollaborationSynthesis(status string, reason string) {
	if metrics == nil {
		return
	}
	switch status {
	case "complete", "partial", "conflict", "insufficient":
	default:
		status = "error"
	}
	switch reason {
	case "all_claims_citation_verified", "partial_agent_results", "evidence_conflict_requires_user_review", "no_supported_claims":
	default:
		reason = "error"
	}
	metrics.collaborationSyntheses.WithLabelValues(status, reason).Inc()
}

func (metrics *Metrics) RecordPolicyLoad(source string, result string) {
	if metrics == nil {
		return
	}
	if source != "redis" && source != "mysql" {
		source = "mysql"
	}
	switch result {
	case "success", "cache_degraded", "invalid", "error":
	default:
		result = "error"
	}
	metrics.policyLoads.WithLabelValues(source, result).Inc()
}

func (metrics *Metrics) SetStrategyWeight(policyVersion string, strategy string, weightBasis int) {
	if metrics == nil {
		return
	}
	strategy = boundedRoutingStrategy(strategy)
	intent := boundedStrategyIntent(strategy)
	policyVersion = boundedRoutingPolicyVersion(policyVersion)
	if weightBasis < 0 {
		weightBasis = 0
	}
	if weightBasis > 10_000 {
		weightBasis = 10_000
	}
	metrics.strategyWeights.WithLabelValues(intent, strategy, policyVersion).Set(float64(weightBasis))
}

func boundedStrategyIntent(strategy string) string {
	switch strategy {
	case "rag_fast", "rag_deep":
		return "project_qa"
	case "diagnosis_standard", "diagnosis_case_based", "diagnosis_collaborative":
		return "troubleshooting"
	case "legacy_chat", "direct_fallback":
		return "general"
	default:
		return "unknown"
	}
}

func boundedRoutingStrategy(value string) string {
	switch value {
	case "legacy_chat", "rag_fast", "rag_deep", "diagnosis_standard", "diagnosis_case_based", "diagnosis_collaborative", "direct_fallback":
		return value
	default:
		return "unknown"
	}
}

func boundedRoutingPolicyVersion(value string) string {
	if value == "routing-policy-v1" {
		return value
	}
	return "other"
}

// RecordLegacyEntryAttempt observes removed public surfaces without restoring
// their handlers or exposing caller-controlled paths as metric labels.
func (metrics *Metrics) RecordLegacyEntryAttempt(entry string) {
	if metrics == nil {
		return
	}
	if entry != "skill_api" {
		entry = "unknown"
	}
	metrics.legacyEntryAttempts.WithLabelValues(entry).Inc()
}

func (metrics *Metrics) RecordToolValidation(tool string, result string) {
	if metrics == nil {
		return
	}
	metrics.toolValidation.WithLabelValues(boundedToolName(tool), boundedToolValidation(result)).Inc()
}

func (metrics *Metrics) RecordToolCall(tool string, strategy string, status string, duration time.Duration) {
	if metrics == nil {
		return
	}
	if duration < 0 {
		duration = 0
	}
	tool, strategy, status = boundedToolName(tool), boundedToolStrategy(strategy), boundedToolStatus(status)
	metrics.toolCalls.WithLabelValues(tool, strategy, status).Inc()
	metrics.toolDuration.WithLabelValues(tool, strategy).Observe(duration.Seconds())
}

func (metrics *Metrics) RecordToolCancellation(tool string, reason string) {
	if metrics == nil {
		return
	}
	switch reason {
	case "timeout", "request_cancelled":
	default:
		reason = "unknown"
	}
	metrics.toolCancellations.WithLabelValues(boundedToolName(tool), reason).Inc()
}

func (metrics *Metrics) RecordToolAuditFailure(tool string) {
	if metrics == nil {
		return
	}
	metrics.persistFailures.WithLabelValues("tool_audit").Inc()
}

func (metrics *Metrics) RecordToolRetry(tool string, reason string) {
	if metrics == nil {
		return
	}
	switch reason {
	case "timeout", "temporary_error":
	default:
		reason = "unknown"
	}
	metrics.toolRetries.WithLabelValues(boundedToolName(tool), reason).Inc()
}

func (metrics *Metrics) RecordToolCache(tool string, result string) {
	if metrics == nil {
		return
	}
	switch result {
	case "hit", "miss", "stale_fallback", "bypass", "store_error":
	default:
		result = "bypass"
	}
	metrics.toolCache.WithLabelValues(boundedToolName(tool), result).Inc()
}

func (metrics *Metrics) SetToolCircuitState(tool string, state string) {
	if metrics == nil {
		return
	}
	tool = boundedToolName(tool)
	if state != "closed" && state != "open" && state != "half_open" {
		state = "open"
	}
	for _, candidate := range []string{"closed", "open", "half_open"} {
		value := 0.0
		if candidate == state {
			value = 1
		}
		metrics.toolCircuitState.WithLabelValues(tool, candidate).Set(value)
	}
}

func boundedToolName(value string) string {
	switch value {
	case "deployment_manifest_lookup", "service_health_snapshot", "bounded_log_signature", "mcp_deployment_evidence", "official_document_search", "confirm_resolution":
		return value
	default:
		return "unknown"
	}
}

func boundedToolStrategy(value string) string {
	switch value {
	case "tool_primary", "tool_agent_v1", "diagnosis_standard", "human_confirmed_action_v1":
		return value
	default:
		return "unknown"
	}
}

func boundedToolStatus(value string) string {
	switch value {
	case "success", "rejected", "invalid_arguments", "budget_exceeded", "timeout", "cancelled", "error":
		return value
	default:
		return "error"
	}
}

func boundedToolValidation(value string) string {
	switch value {
	case "accepted", "registry_miss", "invalid_arguments", "intent_denied", "permission_denied", "side_effect_denied", "budget_exceeded":
		return value
	default:
		return "unknown"
	}
}

func (metrics *Metrics) RecordProfileRecall(status string, duration time.Duration, count int) {
	if metrics == nil {
		return
	}
	switch status {
	case "hit", "no_match", "unavailable":
	default:
		status = "unavailable"
	}
	if duration < 0 {
		duration = 0
	}
	if count < 0 {
		count = 0
	}
	if count > 5 {
		count = 5
	}
	metrics.profileMemoryRecalls.WithLabelValues(status).Inc()
	metrics.profileMemoryRecallDuration.WithLabelValues(status).Observe(duration.Seconds())
	metrics.profileMemoryRecallResults.WithLabelValues(status).Observe(float64(count))
	metrics.memoryRecall.WithLabelValues("profile", status).Inc()
	metrics.memoryCandidates.WithLabelValues("profile").Observe(float64(count))
}

// RecordCaseRecall implements diagnostic.CaseRecallObserver without importing
// diagnostic, keeping the observability package below the workflow layer.
func (metrics *Metrics) RecordCaseRecall(status string, duration time.Duration, count int) {
	if metrics == nil {
		return
	}
	switch status {
	case "hit", "no_match", "unavailable":
	default:
		status = "unavailable"
	}
	if duration < 0 {
		duration = 0
	}
	if count < 0 {
		count = 0
	}
	if count > 3 {
		count = 3
	}
	metrics.caseMemoryRecalls.WithLabelValues(status).Inc()
	metrics.caseMemoryRecallDuration.WithLabelValues(status).Observe(duration.Seconds())
	metrics.caseMemoryRecallResults.WithLabelValues(status).Observe(float64(count))
	metrics.memoryRecall.WithLabelValues("episodic", status).Inc()
	metrics.memoryCandidates.WithLabelValues("episodic").Observe(float64(count))
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
	if created {
		metrics.agentActiveRuns.WithLabelValues(boundedAgentState(run.State)).Inc()
	}
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
	metrics.agentRunTransitions.WithLabelValues(boundedAgentState(previous.State), boundedAgentState(current.State), "success").Inc()
	metrics.agentActiveRuns.WithLabelValues(boundedAgentState(previous.State)).Dec()
	if !harness.IsTerminal(current.State) {
		metrics.agentActiveRuns.WithLabelValues(boundedAgentState(current.State)).Inc()
	}
	if !harness.IsTerminal(current.State) {
		return
	}
	reason := boundedHarnessTerminalReason(current.TerminalReason)
	if reason == "NO_PROGRESS" {
		metrics.agentNoProgress.WithLabelValues("DiagnosticAgent", strategy).Inc()
	}
	if reason == "TIME_BUDGET_EXCEEDED" {
		metrics.agentBudgetExceeded.WithLabelValues("time").Inc()
	}
	if reason == "EXECUTION_BUDGET_EXCEEDED" {
		metrics.agentBudgetExceeded.WithLabelValues("iteration").Inc()
	}
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

func boundedAgentState(value harness.State) string {
	switch value {
	case harness.StateReceived:
		return "received"
	case harness.StateContextReady:
		return "context_ready"
	case harness.StatePlanned:
		return "planned"
	case harness.StateRunning:
		return "running"
	case harness.StateWaitingUser:
		return "waiting_user"
	case harness.StateSucceeded:
		return "succeeded"
	case harness.StateFailed:
		return "failed"
	case harness.StateCancelled:
		return "cancelled"
	case harness.StateBudgetExceeded:
		return "budget_exceeded"
	default:
		return "unknown"
	}
}

func boundedHarnessTerminalReason(value string) string {
	switch value {
	case "DIAGNOSTIC_HYPOTHESES_READY", "USER_CANCELLED", "REQUEST_CONTEXT_CANCELLED", "TIME_BUDGET_EXCEEDED", "EXECUTION_BUDGET_EXCEEDED", "NO_PROGRESS":
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
	if duration < 0 {
		duration = 0
	}
	metrics.ragStrategyRequests.WithLabelValues(strategy, status, enhancement).Inc()
	metrics.ragStrategyDuration.WithLabelValues(strategy, status).Observe(duration.Seconds())
	metrics.ragQueries.WithLabelValues(strategy, status).Inc()
	metrics.ragDuration.WithLabelValues("end_to_end", strategy).Observe(duration.Seconds())
	if status == "insufficient" || status == "verifier_rejected" || status == "rejected" || status == "error" {
		metrics.ragEmpty.WithLabelValues(strategy, boundedRAGEmptyReason(status)).Inc()
	}
	if rewriteOutcome != "" && rewriteOutcome != "none" {
		outcome := boundedEnhancementOutcome(rewriteOutcome)
		metrics.ragEnhancements.WithLabelValues("rewrite", outcome).Inc()
		metrics.ragRewrite.WithLabelValues(boundedRAGEnhancementResult(outcome)).Inc()
	} else {
		metrics.ragRewrite.WithLabelValues("skipped").Inc()
	}
	if rerankOutcome != "" && rerankOutcome != "none" {
		outcome := boundedEnhancementOutcome(rerankOutcome)
		metrics.ragEnhancements.WithLabelValues("rerank", outcome).Inc()
		metrics.ragRerank.WithLabelValues(boundedRAGEnhancementResult(outcome)).Inc()
	} else {
		metrics.ragRerank.WithLabelValues("skipped").Inc()
	}
}

func boundedRAGEmptyReason(status string) string {
	switch status {
	case "insufficient", "verifier_rejected", "rejected":
		return status
	default:
		return "error"
	}
}

func boundedRAGEnhancementResult(outcome string) string {
	switch outcome {
	case "rewrite_completed", "rerank_completed":
		return "completed"
	case "rewrite_not_required", "rerank_not_required":
		return "not_required"
	case "rewrite_model_error", "rewrite_timeout", "rewrite_invalid_output", "rerank_model_error", "rerank_timeout", "rerank_invalid_output":
		return "failed"
	default:
		return "unknown"
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
	verification := "rejected"
	if status == "answered" && gateReason == "sufficient" {
		verification = "verified"
	}
	metrics.citations.WithLabelValues(verification).Inc()
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
	metrics.ragCandidates.WithLabelValues(boundedRAGCandidateSource(mode)).Observe(float64(resultCount))
}

func boundedRAGCandidateSource(mode string) string {
	switch mode {
	case "dense_only":
		return "dense"
	case "bm25_only":
		return "bm25"
	default:
		return "fused"
	}
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
