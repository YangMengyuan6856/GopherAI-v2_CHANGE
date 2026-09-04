package observability

import (
	"GopherAI/internal/app"
	"GopherAI/internal/contract"
	"GopherAI/internal/harness"
	intentdomain "GopherAI/internal/intent"
	"GopherAI/model"
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type memoryRunRepository struct {
	runs []model.AgentRun
	err  error
}

func (repository *memoryRunRepository) Create(run *model.AgentRun) error {
	repository.runs = append(repository.runs, *run)
	return repository.err
}

func testChatOutput() app.ChatOutput {
	startedAt := time.Date(2026, 9, 4, 8, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(1250 * time.Millisecond)
	return app.ChatOutput{
		Request:  contract.RequestContext{TraceID: "trace-1", RequestID: "request-1", UserID: "private-user", StartedAt: startedAt},
		Intent:   contract.IntentResult{Intent: "legacy", Confidence: 1, Version: "legacy-v0", Stages: []contract.IntentStageResult{{Stage: "fixed"}}},
		Decision: contract.StrategyDecision{StrategyName: "legacy_chat", StrategyVersion: "legacy-v0", PolicyVersion: "policy-v0"},
		Result:   contract.AgentResult{SessionID: "session-1"},
		Trace:    contract.TraceEnvelope{TraceID: "trace-1", RequestID: "request-1", StartedAt: startedAt, FinishedAt: finishedAt},
	}
}

func TestRecorderPersistsSanitizedRunAndMetrics(t *testing.T) {
	repository := new(memoryRunRepository)
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry, registry)
	var logs bytes.Buffer
	recorder := NewRecorder(repository, metrics, log.New(&logs, "", 0))
	recorder.Record(testChatOutput(), nil)

	if len(repository.runs) != 1 {
		t.Fatalf("expected one run, got %d", len(repository.runs))
	}
	run := repository.runs[0]
	if run.UserIDHash == "private-user" || len(run.UserIDHash) != 64 {
		t.Fatalf("user identifier was not hashed: %q", run.UserIDHash)
	}
	if run.DurationMicros != 1_250_000 || run.Status != statusSuccess {
		t.Fatalf("unexpected run: %#v", run)
	}
	if strings.Contains(logs.String(), "private-user") {
		t.Fatal("raw user identifier leaked into structured log")
	}
	if count := testutil.ToFloat64(metrics.requests.WithLabelValues("legacy", "legacy_chat", "success")); count != 1 {
		t.Fatalf("expected request counter 1, got %v", count)
	}
	if count := testutil.ToFloat64(metrics.agentRuns.WithLabelValues("LegacyAdapter", "legacy_chat", "success")); count != 1 {
		t.Fatalf("expected distinct agent and strategy labels, got %v", count)
	}
}

func TestRecorderPersistsAndMeasuresShadowDecisionSeparately(t *testing.T) {
	repository := new(memoryRunRepository)
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry, registry)
	output := testChatOutput()
	output.ShadowIntent = &intentdomain.CascadeDecision{
		Result: contract.IntentResult{Intent: intentdomain.Troubleshooting, Confidence: .93, Version: intentdomain.CascadeVersion,
			Stages: []contract.IntentStageResult{{Stage: "llm", ReasonCode: "llm_structured_decision"}}},
		Diagnostics: intentdomain.CascadeDiagnostics{FinalStage: "llm", PrototypeCalled: true, LLMCalled: true, LatencyMillis: 321,
			LLMUsage: contract.ModelUsage{InputTokens: 12, OutputTokens: 4}},
	}
	NewRecorder(repository, metrics, nil).Record(output, nil)

	run := repository.runs[0]
	if run.Intent != "legacy" || run.Strategy != "legacy_chat" || run.ShadowIntent != intentdomain.Troubleshooting {
		t.Fatalf("live and shadow decisions were not kept separate: %+v", run)
	}
	if run.ShadowFinalStage != "llm" || run.ShadowReasonCode != "llm_structured_decision" || run.ShadowLatencyMicros != 321000 {
		t.Fatalf("shadow diagnostics were not persisted: %+v", run)
	}
	if count := testutil.ToFloat64(metrics.intentShadowDecisions.WithLabelValues("troubleshooting", "llm", "success")); count != 1 {
		t.Fatalf("expected one shadow decision, got %v", count)
	}
	if count := testutil.ToFloat64(metrics.intentShadowStageCalls.WithLabelValues("prototype", "fallback")); count != 1 {
		t.Fatalf("expected prototype fallback call, got %v", count)
	}
	if count := testutil.ToFloat64(metrics.intentShadowStageCalls.WithLabelValues("llm", "selected")); count != 1 {
		t.Fatalf("expected selected llm call, got %v", count)
	}
	if count := testutil.ToFloat64(metrics.intentShadowDisagreements.WithLabelValues("legacy_chat", "troubleshooting")); count != 1 {
		t.Fatalf("expected live/shadow disagreement, got %v", count)
	}
}

func TestAgentForStrategyUsesStableLowCardinalityLabels(t *testing.T) {
	for strategy, want := range map[string]string{
		"rag_fast": "KnowledgeAgent", "legacy_chat": "LegacyAdapter",
		"diagnosis_standard": "DiagnosticAgent", "tool_primary": "ToolAgent", "arbitrary-user-value": unknownLabel,
	} {
		if got := agentForStrategy(strategy); got != want {
			t.Fatalf("strategy %q mapped to agent %q, want %q", strategy, got, want)
		}
	}
}

func TestRecorderClassifiesFailureWithoutLeakingInternalError(t *testing.T) {
	repository := new(memoryRunRepository)
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry, registry)
	var logs bytes.Buffer
	recorder := NewRecorder(repository, metrics, log.New(&logs, "", 0))
	output := testChatOutput()
	recorder.Record(output, errors.New("secret dependency address"))

	if repository.runs[0].Status != statusError || repository.runs[0].ErrorCode != "INTERNAL_ERROR" {
		t.Fatalf("unexpected error classification: %#v", repository.runs[0])
	}
	if strings.Contains(logs.String(), "secret dependency address") {
		t.Fatal("internal error leaked into structured log")
	}
}

func TestRAGStrategyMetricsUseBoundedLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry, registry)
	metrics.RecordRAGStrategy("rag_deep", "answered", "completed", 2*time.Second, "rewrite_completed", "rerank_completed")
	metrics.RecordRAGStrategy("untrusted", "unexpected", "unexpected", time.Second, "untrusted", "")

	if count := testutil.ToFloat64(metrics.ragStrategyRequests.WithLabelValues("rag_deep", "answered", "completed")); count != 1 {
		t.Fatalf("expected one deep strategy request, got %v", count)
	}
	if count := testutil.ToFloat64(metrics.ragEnhancements.WithLabelValues("rewrite", "rewrite_completed")); count != 1 {
		t.Fatalf("expected rewrite outcome metric, got %v", count)
	}
	if count := testutil.ToFloat64(metrics.ragEnhancements.WithLabelValues("rerank", "rerank_completed")); count != 1 {
		t.Fatalf("expected rerank outcome metric, got %v", count)
	}
	if count := testutil.ToFloat64(metrics.ragStrategyRequests.WithLabelValues("rag_fast", "error", "partial_fallback")); count != 1 {
		t.Fatalf("untrusted labels must collapse to bounded values, got %v", count)
	}
	if count := testutil.ToFloat64(metrics.ragEnhancements.WithLabelValues("rewrite", "unknown")); count != 1 {
		t.Fatalf("untrusted component outcomes must collapse to unknown, got %v", count)
	}
}

func TestCaseRecallMetricsUseBoundedLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry, registry)
	metrics.RecordCaseRecall("hit", 8*time.Millisecond, 2)
	metrics.RecordCaseRecall("untrusted-user-value", -time.Second, 99)

	if count := testutil.ToFloat64(metrics.caseMemoryRecalls.WithLabelValues("hit")); count != 1 {
		t.Fatalf("expected one case-memory hit, got %v", count)
	}
	if count := testutil.ToFloat64(metrics.caseMemoryRecalls.WithLabelValues("unavailable")); count != 1 {
		t.Fatalf("untrusted recall status must collapse to unavailable, got %v", count)
	}
}

func TestProfileRecallMetricsUseBoundedLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry, registry)
	metrics.RecordProfileRecall("hit", 8*time.Millisecond, 2)
	metrics.RecordProfileRecall("untrusted-user-value", -time.Second, 99)

	if count := testutil.ToFloat64(metrics.profileMemoryRecalls.WithLabelValues("hit")); count != 1 {
		t.Fatalf("expected one profile-memory hit, got %v", count)
	}
	if count := testutil.ToFloat64(metrics.profileMemoryRecalls.WithLabelValues("unavailable")); count != 1 {
		t.Fatalf("untrusted recall status must collapse to unavailable, got %v", count)
	}
}

func TestHarnessMetricsTrackLifecycleWithoutIdentifierLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry, registry)
	startedAt := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)
	previous := harness.Run{
		RunID: "must-not-be-a-label", Intent: "troubleshooting", Strategy: "diagnosis_standard",
		State: harness.StateRunning, StartedAt: startedAt,
		Budget: harness.Budget{MaxIterations: 6, UsedIterations: 3, MaxToolCalls: 4, UsedToolCalls: 1, MaxInputTokens: 100, UsedInputTokens: 50, MaxOutputTokens: 100, UsedOutputTokens: 10, MaxCostMicros: 100, UsedCostMicros: 20},
	}
	finishedAt := startedAt.Add(2 * time.Second)
	current := previous
	current.State = harness.StateSucceeded
	current.TerminalReason = "DIAGNOSTIC_HYPOTHESES_READY"
	current.UpdatedAt = finishedAt
	current.FinishedAt = &finishedAt
	metrics.RecordRunCreate(previous, true)
	metrics.RecordRunCreate(previous, false)
	metrics.RecordRunTransition(previous, current)

	if got := testutil.ToFloat64(metrics.harnessRuns.WithLabelValues("troubleshooting", "diagnosis_standard", "created")); got != 1 {
		t.Fatalf("expected one created run, got %v", got)
	}
	if got := testutil.ToFloat64(metrics.harnessRuns.WithLabelValues("troubleshooting", "diagnosis_standard", "idempotent_replay")); got != 1 {
		t.Fatalf("expected one idempotent replay, got %v", got)
	}
	if got := testutil.ToFloat64(metrics.harnessTransitions.WithLabelValues("troubleshooting", "diagnosis_standard", "RUNNING", "SUCCEEDED")); got != 1 {
		t.Fatalf("expected one persisted transition, got %v", got)
	}
	if got := testutil.ToFloat64(metrics.harnessTerminals.WithLabelValues("troubleshooting", "diagnosis_standard", "SUCCEEDED", "DIAGNOSTIC_HYPOTHESES_READY")); got != 1 {
		t.Fatalf("expected one terminal outcome, got %v", got)
	}
}
