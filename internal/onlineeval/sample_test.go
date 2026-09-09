package onlineeval

import (
	"GopherAI/internal/app"
	"GopherAI/internal/contract"
	"strings"
	"testing"
	"time"
)

func TestSamplingRatesAndRiskOverrides(t *testing.T) {
	base := testOutput("legacy_chat")
	base.Result.Resolved = false // legacy zero value must not force every request.
	stable := Decide(base, nil)
	if stable.Forced || stable.RateBasis != StableRateBasis || stable.TrafficClass != TrafficStable {
		t.Fatalf("unexpected stable decision: %+v", stable)
	}
	canary := base
	canary.Decision.ExperimentBucket = "canary-4"
	if got := Decide(canary, nil); got.Forced || got.RateBasis != CanaryRateBasis || got.TrafficClass != TrafficCanary {
		t.Fatalf("unexpected canary decision: %+v", got)
	}
	probing := base
	probing.Decision.ReasonCode = "probing_exploration"
	if got := Decide(probing, nil); got.Forced || got.RateBasis != ProbingRateBasis || got.TrafficClass != TrafficProbing {
		t.Fatalf("unexpected probing decision: %+v", got)
	}
	low := base
	low.Result.Confidence = .4
	if got := Decide(low, nil); !got.Forced || got.RateBasis != ForcedRateBasis || !contains(got.Reasons, "low_confidence") {
		t.Fatalf("low confidence was not forced: %+v", got)
	}
	tool := base
	tool.Result.ToolCalls = []contract.ToolCallResult{{Status: "failed", ErrorCode: "secret-runtime-message"}}
	if got := Decide(tool, nil); !got.Forced || !contains(got.Reasons, "tool_failure") {
		t.Fatalf("tool failure was not forced: %+v", got)
	}
	rag := testOutput("rag_fast")
	rag.Result.Evidence = nil
	if got := Decide(rag, nil); !got.Forced || !contains(got.Reasons, "evidence_gate_failed") {
		t.Fatalf("evidence gate failure was not forced: %+v", got)
	}
}

func TestSamplingBucketIsDeterministic(t *testing.T) {
	output := testOutput("legacy_chat")
	first, second := Decide(output, nil), Decide(output, nil)
	if first.Bucket != second.Bucket || first.Selected != second.Selected {
		t.Fatalf("same request replay changed sampling decision: %+v vs %+v", first, second)
	}
}

func TestBuildSampleRedactsContentAndHashesIdentity(t *testing.T) {
	output := testOutput("rag_fast")
	output.Request.UserID = "raw-user@example.com"
	output.Request.TenantID = "raw-tenant@example.com"
	output.Request.Question = "password=plain-secret Bearer abcdefghijk123456 owner@example.com"
	output.Result.Answer = "token=answer-secret contact@example.com"
	output.Result.Evidence = []contract.Evidence{{ID: "evidence-1", Kind: "knowledge", TenantID: output.Request.TenantID, SourceID: "source-1", SourceVersion: "v1", Title: "owner@example.com", Content: "api_key=evidence-secret", Retrieval: "hybrid"}}
	decision := decide(output, nil, "user_downvote")
	sample, event, err := BuildSample(output, nil, decision, time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.Join([]string{sample.Question, sample.Answer, sample.EvidenceJSON, event.PayloadJSON, sample.UserHash, sample.TenantHash}, " ")
	for _, secret := range []string{"plain-secret", "abcdefghijk123456", "owner@example.com", "answer-secret", "contact@example.com", "evidence-secret", "raw-user@example.com", "raw-tenant@example.com"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("sensitive value persisted in sampled payload: %s", secret)
		}
	}
	if sample.RedactionCount < 6 || len(sample.UserHash) != 64 || len(sample.TenantHash) != 64 {
		t.Fatalf("expected redaction evidence and hashes, got redactions=%d user=%q tenant=%q", sample.RedactionCount, sample.UserHash, sample.TenantHash)
	}
}

func testOutput(strategy string) app.ChatOutput {
	return app.ChatOutput{
		Request:  contract.RequestContext{TraceID: "trace-1", RequestID: "request-1", UserID: "user-1", TenantID: "tenant-1", Question: "如何发布服务？"},
		Intent:   contract.IntentResult{Intent: "general", Version: "intent-v1"},
		Decision: contract.StrategyDecision{StrategyName: strategy, StrategyVersion: "strategy-v1", PolicyVersion: "policy-v1"},
		Result:   contract.AgentResult{Answer: "按照发布手册执行。", Confidence: .9, Resolved: true, Evidence: []contract.Evidence{{ID: "evidence-1", Kind: "knowledge", TenantID: "tenant-1", SourceID: "source-1", SourceVersion: "v1", Title: "发布手册", Content: "发布步骤", Retrieval: "hybrid"}}},
	}
}

func contains(items []string, expected string) bool {
	for _, item := range items {
		if item == expected {
			return true
		}
	}
	return false
}
