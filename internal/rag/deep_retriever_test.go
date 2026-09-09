package rag

import (
	"context"
	"errors"
	"testing"
)

type fakeDeepSearcher struct {
	outputs map[string]SearchOutput
	errors  map[string]error
	inputs  []SearchInput
}

func (searcher *fakeDeepSearcher) Search(_ context.Context, input SearchInput) (SearchOutput, error) {
	searcher.inputs = append(searcher.inputs, input)
	if err := searcher.errors[input.Query]; err != nil {
		return SearchOutput{}, err
	}
	return searcher.outputs[input.Query], nil
}

type fakeDeepRewriter struct {
	result QueryRewriteResult
	calls  int
}

func (rewriter *fakeDeepRewriter) Rewrite(_ context.Context, _ string, _ QueryAssessment) QueryRewriteResult {
	rewriter.calls++
	return rewriter.result
}

type fakeDeepReranker struct {
	result RerankResult
	order  []int
	calls  int
}

func (reranker *fakeDeepReranker) Rerank(_ context.Context, _ string, _ QueryAssessment, hits []SearchHit) ([]SearchHit, RerankResult) {
	reranker.calls++
	if len(reranker.order) == 0 {
		return hits, reranker.result
	}
	ranked := make([]SearchHit, 0, len(hits))
	for _, index := range reranker.order {
		ranked = append(ranked, hits[index])
	}
	return ranked, reranker.result
}

func TestDeepRetrieverSkipsEnhancementForSimpleHighConfidenceQuery(t *testing.T) {
	query := "默认端口是多少"
	base := &fakeDeepSearcher{outputs: map[string]SearchOutput{query: {
		Hits: []SearchHit{deepHit("c1", "d1", "h1", 0.96)},
		Diagnostics: SearchDiagnostics{Mode: "hybrid", DenseCandidates: 1, KeywordCandidates: 1,
			QueryAssessment: QueryAssessment{Version: QueryAssessmentVersion, Complexity: QueryComplexitySimple, Gap: QueryGapNone}},
	}}}
	rewriter := new(fakeDeepRewriter)
	reranker := new(fakeDeepReranker)
	deep, _ := NewDeepRetriever(base, rewriter, reranker)
	output, err := deep.Search(context.Background(), SearchInput{TenantID: "tenant", UserID: "user", Query: query, TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	if output.Diagnostics.Deep == nil || output.Diagnostics.Deep.Activated || output.Diagnostics.Deep.Outcome != DeepOutcomeSkipped {
		t.Fatalf("simple query should retain fast retrieval: %+v", output.Diagnostics.Deep)
	}
	if rewriter.calls != 0 || reranker.calls != 0 || len(base.inputs) != 1 || base.inputs[0].TopK != MaxTopK {
		t.Fatalf("unexpected deep work: searches=%+v rewrite=%d rerank=%d", base.inputs, rewriter.calls, reranker.calls)
	}
}

func TestDeepRetrieverRunsBoundedRewriteMergesDeduplicatesAndReranks(t *testing.T) {
	query := "比较多份文档的重试配置并分析差异"
	base := &fakeDeepSearcher{outputs: map[string]SearchOutput{
		query: {
			Hits: []SearchHit{deepHit("c1", "d1", "hash-1", 0.94)},
			Diagnostics: SearchDiagnostics{Mode: "hybrid", DenseCandidates: 1, KeywordCandidates: 1,
				QueryAssessment: QueryAssessment{Version: QueryAssessmentVersion, Complexity: QueryComplexityComplex, DeepRecommended: true, RewriteRecommended: true, ReasonCodes: []string{"comparison_query"}}},
		},
		"rewrite-a": {Hits: []SearchHit{deepHit("c1-copy", "d1", "hash-1", 0.91), deepHit("c2", "d2", "hash-2", 0.90)}, Diagnostics: SearchDiagnostics{Mode: "hybrid", DenseCandidates: 2, KeywordCandidates: 2}},
		"rewrite-b": {Hits: []SearchHit{deepHit("c3", "d3", "hash-3", 0.87)}, Diagnostics: SearchDiagnostics{Mode: "hybrid", DenseCandidates: 1, KeywordCandidates: 1}},
	}}
	rewriter := &fakeDeepRewriter{result: QueryRewriteResult{
		Status: RewriteStatusCompleted, Triggered: true, Queries: []string{query, "rewrite-a", "rewrite-b"}, OutcomeReason: RewriteReasonCompleted,
	}}
	reranker := &fakeDeepReranker{
		result: RerankResult{Status: RerankStatusCompleted, Triggered: true, OutcomeReason: RerankReasonCompleted},
		order:  []int{1, 0, 2},
	}
	deep, _ := NewDeepRetriever(base, rewriter, reranker)
	output, err := deep.Search(context.Background(), SearchInput{TenantID: "tenant", UserID: "user", Query: query, TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(base.inputs) != DeepMaxRetrievalQueries || rewriter.calls != 1 || reranker.calls != 1 {
		t.Fatalf("deep budget was not respected: searches=%d rewrite=%d rerank=%d", len(base.inputs), rewriter.calls, reranker.calls)
	}
	if len(output.Hits) != 2 || output.Hits[0].Evidence.ID != "c2" || output.Hits[1].Evidence.ID != "c1" {
		t.Fatalf("deduplicated rerank was not applied before TopK: %v", evidenceIDs(output.Hits))
	}
	diagnostics := output.Diagnostics.Deep
	if diagnostics == nil || !diagnostics.Activated || diagnostics.Outcome != DeepOutcomeCompleted || diagnostics.AdditionalSearches != 2 || diagnostics.CandidatesBefore != 1 || diagnostics.CandidatesAfter != 2 {
		t.Fatalf("unexpected deep diagnostics: %+v", diagnostics)
	}
}

func TestDeepRetrieverPreservesBaselineWhenRewriteAndAdditionalSearchFail(t *testing.T) {
	query := "分析为什么配置冲突并给出建议"
	base := &fakeDeepSearcher{
		outputs: map[string]SearchOutput{query: {
			Hits: []SearchHit{deepHit("c1", "d1", "h1", 0.95)},
			Diagnostics: SearchDiagnostics{Mode: "hybrid", DenseCandidates: 1, KeywordCandidates: 1,
				QueryAssessment: QueryAssessment{Version: QueryAssessmentVersion, Complexity: QueryComplexityComplex, DeepRecommended: true, RewriteRecommended: true}},
		}},
		errors: map[string]error{"rewrite-a": errors.New("embedding unavailable")},
	}
	rewriter := &fakeDeepRewriter{result: QueryRewriteResult{
		Status: RewriteStatusCompleted, Triggered: true, Queries: []string{query, "rewrite-a"}, OutcomeReason: RewriteReasonCompleted,
	}}
	reranker := &fakeDeepReranker{result: RerankResult{Status: RerankStatusSkipped, OutcomeReason: RerankReasonNotRequired}}
	deep, _ := NewDeepRetriever(base, rewriter, reranker)
	output, err := deep.Search(context.Background(), SearchInput{TenantID: "tenant", UserID: "user", Query: query})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Hits) != 1 || output.Hits[0].Evidence.ID != "c1" || output.Diagnostics.Deep.Outcome != DeepOutcomePartialFallback || output.Diagnostics.Deep.AdditionalSearchFailures != 1 {
		t.Fatalf("additional failure must retain baseline evidence: output=%v diagnostics=%+v", evidenceIDs(output.Hits), output.Diagnostics.Deep)
	}
}

func TestDeepRetrieverLetsRewriteRecoverEmptyBaseline(t *testing.T) {
	query := "它为什么会这样"
	base := &fakeDeepSearcher{outputs: map[string]SearchOutput{
		query: {
			Hits: []SearchHit{}, Diagnostics: SearchDiagnostics{Mode: "hybrid", QueryAssessment: QueryAssessment{
				Version: QueryAssessmentVersion, Gap: QueryGapHard, DeepRecommended: true, RewriteRecommended: true, ReasonCodes: []string{"no_evidence"},
			}},
		},
		"明确配置名": {Hits: []SearchHit{deepHit("c1", "d1", "h1", 0.97)}, Diagnostics: SearchDiagnostics{Mode: "hybrid", DenseCandidates: 1, KeywordCandidates: 1}},
	}}
	rewriter := &fakeDeepRewriter{result: QueryRewriteResult{Status: RewriteStatusCompleted, Triggered: true, Queries: []string{query, "明确配置名"}}}
	reranker := new(fakeDeepReranker)
	deep, _ := NewDeepRetriever(base, rewriter, reranker)
	output, err := deep.Search(context.Background(), SearchInput{TenantID: "tenant", UserID: "user", Query: query})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Hits) != 1 || output.Hits[0].Evidence.ID != "c1" || output.Diagnostics.Deep.AdditionalSearches != 1 {
		t.Fatalf("rewrite did not recover evidence: %+v", output)
	}
}

func deepHit(id string, sourceID string, hash string, score float64) SearchHit {
	hit := assessmentHit(id, sourceID, score, "dense+bm25")
	hit.Evidence.ContentHash = hash
	hit.Evidence.Content = "authorized evidence " + id
	return hit
}
