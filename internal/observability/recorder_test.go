package observability

import (
	"GopherAI/internal/app"
	"GopherAI/internal/contract"
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
