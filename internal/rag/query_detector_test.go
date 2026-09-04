package rag

import (
	"testing"

	"GopherAI/internal/contract"
)

func TestAssessQueryKeepsSimpleHighConfidenceQueryOnFastPath(t *testing.T) {
	assessment := AssessQuery("REDIS_TIMEOUT 的默认重试次数是多少", []SearchHit{
		assessmentHit("chunk-1", "doc-1", 0.96, "dense+bm25"),
	}, SearchDiagnostics{Mode: "hybrid", DenseCandidates: 3, KeywordCandidates: 2})

	if assessment.Version != QueryAssessmentVersion || assessment.Complexity != QueryComplexitySimple || assessment.Gap != QueryGapNone {
		t.Fatalf("unexpected fast-path classification: %+v", assessment)
	}
	if assessment.DeepRecommended || assessment.RewriteRecommended || assessment.RerankRecommended {
		t.Fatalf("a simple high-confidence query must not pay the deep-path cost: %+v", assessment)
	}
	assertReason(t, assessment.ReasonCodes, "simple_query_high_confidence")
}

func TestAssessQueryExplainsComplexCrossDocumentRecommendation(t *testing.T) {
	assessment := AssessQuery(
		"请比较多个文档中的 Redis 重试策略，然后分析差异以及给出取舍建议",
		[]SearchHit{
			assessmentHit("chunk-1", "doc-1", 0.95, "dense+bm25"),
			assessmentHit("chunk-2", "doc-2", 0.91, "dense+bm25"),
		},
		SearchDiagnostics{Mode: "hybrid", DenseCandidates: 4, KeywordCandidates: 4},
	)

	if assessment.Complexity != QueryComplexityComplex || !assessment.DeepRecommended || !assessment.RewriteRecommended || !assessment.RerankRecommended {
		t.Fatalf("complex cross-document query must recommend bounded enhancement: %+v", assessment)
	}
	for _, reason := range []string{"multi_part_query", "comparison_query", "cross_document_query", "analytical_query", "weak_rank_separation"} {
		assertReason(t, assessment.ReasonCodes, reason)
	}
	if assessment.Signals.DistinctSources != 2 || assessment.Signals.ClauseCount < 3 {
		t.Fatalf("explanation signals were not retained: %+v", assessment.Signals)
	}
}

func TestAssessQueryDetectsAmbiguousNoEvidenceHardGap(t *testing.T) {
	assessment := AssessQuery("它为什么会这样", nil, SearchDiagnostics{Mode: "hybrid"})

	if assessment.Gap != QueryGapHard || !assessment.RewriteRecommended || !assessment.DeepRecommended || assessment.RerankRecommended {
		t.Fatalf("ambiguous empty retrieval must be a rewrite-only hard gap: %+v", assessment)
	}
	assertReason(t, assessment.ReasonCodes, "ambiguous_reference")
	assertReason(t, assessment.ReasonCodes, "no_evidence")
}

func TestAssessQueryDetectsRetrieverAndCrossDocumentGaps(t *testing.T) {
	assessment := AssessQuery(
		"比较多份文档的超时配置",
		[]SearchHit{assessmentHit("chunk-1", "doc-1", 0.92, "bm25")},
		SearchDiagnostics{Mode: "bm25_only", KeywordCandidates: 1},
	)

	if assessment.Gap != QueryGapHard || !assessment.RewriteRecommended {
		t.Fatalf("missing retriever and source coverage must be a hard gap: %+v", assessment)
	}
	for _, reason := range []string{"retrieval_degraded", "single_retriever_evidence", "cross_document_evidence_gap"} {
		assertReason(t, assessment.ReasonCodes, reason)
	}
}

func TestAssessQueryUsesDistinctSourceTieAsRerankSignal(t *testing.T) {
	assessment := AssessQuery(
		"默认端口是多少",
		[]SearchHit{
			assessmentHit("chunk-1", "doc-1", 0.93, "dense+bm25"),
			assessmentHit("chunk-2", "doc-2", 0.90, "dense+bm25"),
		},
		SearchDiagnostics{Mode: "hybrid", DenseCandidates: 2, KeywordCandidates: 2},
	)

	if assessment.Gap != QueryGapNone || assessment.RewriteRecommended || !assessment.RerankRecommended || !assessment.DeepRecommended {
		t.Fatalf("close authoritative sources should recommend rerank only: %+v", assessment)
	}
	assertReason(t, assessment.ReasonCodes, "weak_rank_separation")
}

func TestAssessQueryMarksLowTopScoreAsHardGap(t *testing.T) {
	assessment := AssessQuery(
		"默认重试配置",
		[]SearchHit{assessmentHit("chunk-1", "doc-1", 0.64, "dense+bm25")},
		SearchDiagnostics{Mode: "hybrid", DenseCandidates: 1, KeywordCandidates: 1},
	)
	if assessment.Gap != QueryGapHard || !assessment.RewriteRecommended {
		t.Fatalf("low evidence must recommend rewrite: %+v", assessment)
	}
	assertReason(t, assessment.ReasonCodes, "low_top_score")
}

func assessmentHit(id string, sourceID string, score float64, retrieval string) SearchHit {
	return SearchHit{Evidence: contract.Evidence{ID: id, SourceID: sourceID, Score: score, Retrieval: retrieval}}
}

func assertReason(t *testing.T, reasons []string, expected string) {
	t.Helper()
	for _, reason := range reasons {
		if reason == expected {
			return
		}
	}
	t.Fatalf("reason %q not found in %v", expected, reasons)
}
