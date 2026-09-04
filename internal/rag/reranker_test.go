package rag

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestConditionalRerankerSkipsWithoutRecommendation(t *testing.T) {
	model := new(fakeRewriteModel)
	reranker, _ := NewConditionalReranker(model, time.Second)
	hits := []SearchHit{assessmentHit("c1", "d1", 0.9, "dense+bm25"), assessmentHit("c2", "d2", 0.8, "dense+bm25")}
	ranked, result := reranker.Rerank(context.Background(), "query", QueryAssessment{}, hits)
	if result.Triggered || result.Status != RerankStatusSkipped || model.calls != 0 || ranked[0].Evidence.ID != "c1" {
		t.Fatalf("rerank should skip without a recommendation: result=%+v calls=%d ranked=%+v", result, model.calls, evidenceIDs(ranked))
	}
}

func TestConditionalRerankerAcceptsCompleteKnownPermutation(t *testing.T) {
	model := &fakeRewriteModel{response: schema.AssistantMessage(`{"ranking":["c2","c1","c3"]}`, nil)}
	reranker, _ := NewConditionalReranker(model, time.Second)
	hits := []SearchHit{
		assessmentHit("c1", "d1", 0.93, "dense+bm25"),
		assessmentHit("c2", "d2", 0.91, "dense+bm25"),
		assessmentHit("c3", "d3", 0.84, "dense+bm25"),
	}
	ranked, result := reranker.Rerank(context.Background(), "compare", QueryAssessment{RerankRecommended: true}, hits)
	if result.Status != RerankStatusCompleted || !result.Triggered || result.OutcomeReason != RerankReasonCompleted {
		t.Fatalf("unexpected rerank result: %+v", result)
	}
	if got := evidenceIDs(ranked); len(got) != 3 || got[0] != "c2" || got[1] != "c1" || got[2] != "c3" {
		t.Fatalf("valid permutation was not applied: %v", got)
	}
	if len(result.OriginalEvidenceIDs) != len(result.RankedEvidenceIDs) {
		t.Fatalf("rerank must never drop evidence: %+v", result)
	}
}

func TestConditionalRerankerRejectsUnknownDuplicateAndMissingIDs(t *testing.T) {
	hits := []SearchHit{assessmentHit("c1", "d1", 0.9, "dense+bm25"), assessmentHit("c2", "d2", 0.89, "dense+bm25")}
	for name, output := range map[string]string{
		"unknown":   `{"ranking":["c2","other"]}`,
		"duplicate": `{"ranking":["c2","c2"]}`,
		"missing":   `{"ranking":["c2"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			model := &fakeRewriteModel{response: schema.AssistantMessage(output, nil)}
			reranker, _ := NewConditionalReranker(model, time.Second)
			ranked, result := reranker.Rerank(context.Background(), "query", QueryAssessment{RerankRecommended: true}, hits)
			if result.Status != RerankStatusFallback || result.OutcomeReason != RerankReasonInvalidOutput || ranked[0].Evidence.ID != "c1" || len(ranked) != len(hits) {
				t.Fatalf("invalid ranking must preserve RRF order: result=%+v ranked=%v", result, evidenceIDs(ranked))
			}
		})
	}
}

func TestConditionalRerankerTimesOutWithoutLosingEvidence(t *testing.T) {
	model := &fakeRewriteModel{wait: true}
	reranker, _ := NewConditionalReranker(model, 10*time.Millisecond)
	hits := []SearchHit{assessmentHit("c1", "d1", 0.9, "dense+bm25"), assessmentHit("c2", "d2", 0.89, "dense+bm25")}
	ranked, result := reranker.Rerank(context.Background(), "query", QueryAssessment{RerankRecommended: true}, hits)
	if result.Status != RerankStatusFallback || result.OutcomeReason != RerankReasonTimeout || len(ranked) != 2 || ranked[0].Evidence.ID != "c1" || result.LatencyMillis < 1 {
		t.Fatalf("timeout must retain every RRF candidate: result=%+v ranked=%v", result, evidenceIDs(ranked))
	}
}

func TestConditionalRerankerTruncatesUntrustedEvidenceInPrompt(t *testing.T) {
	model := &fakeRewriteModel{response: schema.AssistantMessage(`{"ranking":["c1","c2"]}`, nil)}
	reranker, _ := NewConditionalReranker(model, time.Second)
	hits := []SearchHit{assessmentHit("c1", "d1", 0.9, "dense+bm25"), assessmentHit("c2", "d2", 0.89, "dense+bm25")}
	hits[0].Evidence.Content = strings.Repeat("证", maxRerankEvidenceRunes+500)
	_, result := reranker.Rerank(context.Background(), "query", QueryAssessment{RerankRecommended: true}, hits)
	if result.Status != RerankStatusCompleted || len(model.input) != 2 || len([]rune(model.input[1].Content)) > maxRerankEvidenceRunes+1500 {
		t.Fatalf("rerank prompt was not bounded: result=%+v prompt_runes=%d", result, len([]rune(model.input[1].Content)))
	}
}
