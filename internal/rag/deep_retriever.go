package rag

import (
	"GopherAI/internal/contract"
	"context"
	"errors"
	"strings"
)

const (
	DeepStrategyName           = "rag_deep"
	DeepStrategyVersion        = "rag-deep-v1"
	DeepRetrievalVersion       = "rag-deep-retrieval-v1"
	DeepOutcomeSkipped         = "skipped"
	DeepOutcomeCompleted       = "completed"
	DeepOutcomePartialFallback = "partial_fallback"

	DeepMaxRetrievalQueries = 3
	DeepMaxRewriteCalls     = 1
	DeepMaxRerankCalls      = 1
)

type Searcher interface {
	Search(ctx context.Context, input SearchInput) (SearchOutput, error)
}

type QueryRewriteExecutor interface {
	Rewrite(ctx context.Context, query string, assessment QueryAssessment) QueryRewriteResult
}

type EvidenceRerankExecutor interface {
	Rerank(ctx context.Context, query string, assessment QueryAssessment, hits []SearchHit) ([]SearchHit, RerankResult)
}

type DeepRetrievalBudget struct {
	MaxRetrievalQueries int `json:"max_retrieval_queries"`
	MaxRewriteCalls     int `json:"max_rewrite_calls"`
	MaxRerankCalls      int `json:"max_rerank_calls"`
}

type DeepSearchDiagnostics struct {
	Version                  string              `json:"version"`
	Activated                bool                `json:"activated"`
	Outcome                  string              `json:"outcome"`
	Budget                   DeepRetrievalBudget `json:"budget"`
	Rewrite                  QueryRewriteResult  `json:"rewrite"`
	Rerank                   RerankResult        `json:"rerank"`
	AdditionalSearches       int                 `json:"additional_searches"`
	AdditionalSearchFailures int                 `json:"additional_search_failures"`
	CandidatesBefore         int                 `json:"candidates_before"`
	CandidatesAfter          int                 `json:"candidates_after"`
	FallbackReasons          []string            `json:"fallback_reasons,omitempty"`
	Usage                    contract.ModelUsage `json:"usage"`
}

type DeepRetriever struct {
	base     Searcher
	rewriter QueryRewriteExecutor
	reranker EvidenceRerankExecutor
}

func NewDeepRetriever(base Searcher, rewriter QueryRewriteExecutor, reranker EvidenceRerankExecutor) (*DeepRetriever, error) {
	if base == nil || rewriter == nil || reranker == nil {
		return nil, errors.New("base retriever, query rewriter and evidence reranker are required")
	}
	return &DeepRetriever{base: base, rewriter: rewriter, reranker: reranker}, nil
}

func (retriever *DeepRetriever) Search(ctx context.Context, input SearchInput) (SearchOutput, error) {
	if retriever == nil || retriever.base == nil || retriever.rewriter == nil || retriever.reranker == nil {
		return SearchOutput{}, ErrRetrievalUnavailable
	}
	finalTopK := input.TopK
	if finalTopK == 0 {
		finalTopK = DefaultTopK
	}
	if finalTopK < 1 || finalTopK > MaxTopK {
		return SearchOutput{}, ErrInvalidSearch
	}
	baselineInput := input
	baselineInput.TopK = MaxTopK
	baseline, err := retriever.base.Search(ctx, baselineInput)
	if err != nil {
		return SearchOutput{}, err
	}
	diagnostics := &DeepSearchDiagnostics{
		Version: DeepRetrievalVersion, Outcome: DeepOutcomeSkipped,
		Budget:           DeepRetrievalBudget{MaxRetrievalQueries: DeepMaxRetrievalQueries, MaxRewriteCalls: DeepMaxRewriteCalls, MaxRerankCalls: DeepMaxRerankCalls},
		CandidatesBefore: len(baseline.Hits),
	}
	assessment := baseline.Diagnostics.QueryAssessment
	if assessment.Version == "" {
		assessment = AssessQuery(input.Query, baseline.Hits, baseline.Diagnostics)
		baseline.Diagnostics.QueryAssessment = assessment
	}
	if !assessment.DeepRecommended {
		diagnostics.CandidatesAfter = min(finalTopK, len(baseline.Hits))
		baseline.Diagnostics.Deep = diagnostics
		baseline.Hits = limitHits(baseline.Hits, finalTopK)
		baseline.Diagnostics.FusedCandidates = len(baseline.Hits)
		baseline.Conflicts = DetectEvidenceConflicts(baseline.Hits)
		baseline.Diagnostics.ConflictVersion = EvidenceConflictVersion
		baseline.Diagnostics.ValidConflicts = len(baseline.Conflicts)
		return baseline, nil
	}
	diagnostics.Activated = true
	diagnostics.Outcome = DeepOutcomeCompleted
	diagnostics.Rewrite = retriever.rewriter.Rewrite(ctx, input.Query, assessment)
	addUsage(&diagnostics.Usage, diagnostics.Rewrite.Usage.InputTokens, diagnostics.Rewrite.Usage.OutputTokens, diagnostics.Rewrite.Usage.CostMicros)
	if diagnostics.Rewrite.Triggered && diagnostics.Rewrite.Status == RewriteStatusFallback {
		diagnostics.Outcome = DeepOutcomePartialFallback
		diagnostics.FallbackReasons = append(diagnostics.FallbackReasons, diagnostics.Rewrite.OutcomeReason)
	}

	combined := append([]SearchHit(nil), baseline.Hits...)
	combinedDiagnostics := baseline.Diagnostics
	for _, rewrittenQuery := range additionalQueries(diagnostics.Rewrite.Queries) {
		if diagnostics.AdditionalSearches >= DeepMaxRetrievalQueries-1 {
			break
		}
		diagnostics.AdditionalSearches++
		searchInput := baselineInput
		searchInput.Query = rewrittenQuery
		rewritten, searchErr := retriever.base.Search(ctx, searchInput)
		if searchErr != nil {
			diagnostics.AdditionalSearchFailures++
			diagnostics.Outcome = DeepOutcomePartialFallback
			diagnostics.FallbackReasons = appendUniqueString(diagnostics.FallbackReasons, "rewrite_retrieval_failed")
			continue
		}
		combined = mergeSearchHits(combined, rewritten.Hits)
		combinedDiagnostics.DenseCandidates += rewritten.Diagnostics.DenseCandidates
		combinedDiagnostics.KeywordCandidates += rewritten.Diagnostics.KeywordCandidates
		combinedDiagnostics.DegradedReasons = appendUniqueStrings(combinedDiagnostics.DegradedReasons, rewritten.Diagnostics.DegradedReasons...)
		if combinedDiagnostics.Mode != "hybrid" && rewritten.Diagnostics.Mode == "hybrid" {
			combinedDiagnostics.Mode = "hybrid"
		}
	}
	combinedDiagnostics.FusedCandidates = len(combined)
	assessment = AssessQuery(input.Query, combined, combinedDiagnostics)
	combinedDiagnostics.QueryAssessment = assessment
	ranked, rerankResult := retriever.reranker.Rerank(ctx, input.Query, assessment, combined)
	diagnostics.Rerank = rerankResult
	addUsage(&diagnostics.Usage, rerankResult.Usage.InputTokens, rerankResult.Usage.OutputTokens, rerankResult.Usage.CostMicros)
	if rerankResult.Triggered && rerankResult.Status == RerankStatusFallback {
		diagnostics.Outcome = DeepOutcomePartialFallback
		diagnostics.FallbackReasons = appendUniqueString(diagnostics.FallbackReasons, rerankResult.OutcomeReason)
	}
	ranked = limitHits(ranked, finalTopK)
	diagnostics.CandidatesAfter = len(ranked)
	combinedDiagnostics.FusedCandidates = len(ranked)
	combinedDiagnostics.Deep = diagnostics
	conflicts := DetectEvidenceConflicts(ranked)
	combinedDiagnostics.ConflictVersion = EvidenceConflictVersion
	combinedDiagnostics.ValidConflicts = len(conflicts)
	return SearchOutput{Hits: ranked, Diagnostics: combinedDiagnostics, Conflicts: conflicts}, nil
}

func additionalQueries(queries []string) []string {
	if len(queries) <= 1 {
		return nil
	}
	return queries[1:]
}

func mergeSearchHits(existing []SearchHit, additional []SearchHit) []SearchHit {
	result := append([]SearchHit(nil), existing...)
	seen := make(map[string]struct{}, len(existing)+len(additional))
	for _, hit := range existing {
		seen[searchHitIdentity(hit)] = struct{}{}
	}
	for _, hit := range additional {
		identity := searchHitIdentity(hit)
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		result = append(result, hit)
		if len(result) == MaxTopK {
			break
		}
	}
	return result
}

func searchHitIdentity(hit SearchHit) string {
	if hash := strings.TrimSpace(hit.Evidence.ContentHash); hash != "" {
		return "hash:" + hash
	}
	return "id:" + strings.TrimSpace(hit.Evidence.ID)
}

func limitHits(hits []SearchHit, limit int) []SearchHit {
	if limit >= len(hits) {
		return hits
	}
	return hits[:limit]
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueStrings(values []string, additions ...string) []string {
	for _, addition := range additions {
		values = appendUniqueString(values, addition)
	}
	return values
}

func addUsage(usage *contract.ModelUsage, inputTokens int, outputTokens int, costMicros int64) {
	if usage == nil {
		return
	}
	usage.InputTokens += inputTokens
	usage.OutputTokens += outputTokens
	usage.CostMicros += costMicros
}
