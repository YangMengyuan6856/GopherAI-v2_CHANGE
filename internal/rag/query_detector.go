package rag

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	QueryAssessmentVersion = "query-gap-v1"

	QueryComplexitySimple  = "simple"
	QueryComplexityComplex = "complex"
	QueryGapNone           = "none"
	QueryGapSoft           = "soft"
	QueryGapHard           = "hard"

	complexityThreshold  = 0.45
	lowEvidenceThreshold = 0.80
	weakRankMargin       = 0.06
)

var (
	comparisonPattern         = regexp.MustCompile(`(?i)(比较|对比|区别|差异|优缺点|取舍|versus|\bvs\.?\b|compare|difference|trade-?off)`)
	crossDocumentPattern      = regexp.MustCompile(`(?i)(跨文档|多个文档|多份文档|结合.{0,6}(文档|资料)|分别根据|across\s+(documents|files)|multiple\s+(documents|files))`)
	causalPattern             = regexp.MustCompile(`(?i)(为什么|为何|原因|根因|导致|影响|因果|why|cause|root\s+cause|impact)`)
	analyticalPattern         = regexp.MustCompile(`(?i)(分析|诊断|排查|方案|步骤|建议|权衡|analy[sz]e|diagnos|troubleshoot|recommend|step)`)
	ambiguousReferencePattern = regexp.MustCompile(`(?i)^\s*(它|其|前者|后者|上述(?:内容|方案)?|这个|那个|it|this|that)(的|是|怎么|为什么|如何|是否|在哪里|指什么|\s+(is|does|has|was|why|how|where))`)
	englishConnectorPattern   = regexp.MustCompile(`(?i)\b(and|also|then|while|plus)\b`)
	clauseSeparatorPattern    = regexp.MustCompile(`[?？;；。]+`)
)

// QueryAssessment is a deterministic, bounded explanation of whether a later
// RAG strategy may benefit from rewrite or rerank. It recommends work only;
// strategy activation remains a separate policy decision.
type QueryAssessment struct {
	Version            string                 `json:"version"`
	Complexity         string                 `json:"complexity"`
	ComplexityScore    float64                `json:"complexity_score"`
	Gap                string                 `json:"gap"`
	DeepRecommended    bool                   `json:"deep_recommended"`
	RewriteRecommended bool                   `json:"rewrite_recommended"`
	RerankRecommended  bool                   `json:"rerank_recommended"`
	ReasonCodes        []string               `json:"reason_codes"`
	Signals            QueryAssessmentSignals `json:"signals"`
}

type QueryAssessmentSignals struct {
	RuneCount           int     `json:"rune_count"`
	ClauseCount         int     `json:"clause_count"`
	Comparison          bool    `json:"comparison"`
	CrossDocument       bool    `json:"cross_document"`
	Causal              bool    `json:"causal"`
	Analytical          bool    `json:"analytical"`
	AmbiguousReference  bool    `json:"ambiguous_reference"`
	TopScore            float64 `json:"top_score"`
	ScoreMargin         float64 `json:"score_margin"`
	DistinctSources     int     `json:"distinct_sources"`
	HybridEvidenceCount int     `json:"hybrid_evidence_count"`
}

// AssessQuery combines query-shape signals with actual retrieval evidence.
// It deliberately avoids an LLM call so the decision is reproducible, cheap,
// and suitable as the gate in front of conditional rewrite/rerank.
func AssessQuery(query string, hits []SearchHit, diagnostics SearchDiagnostics) QueryAssessment {
	assessment := AnalyzeQueryShape(query)

	populateRetrievalSignals(&assessment.Signals, hits)
	if assessment.Signals.AmbiguousReference {
		assessment.Gap = QueryGapHard
		assessment.RewriteRecommended = true
	}
	if len(hits) == 0 {
		assessment.Gap = QueryGapHard
		assessment.RewriteRecommended = true
		assessment.ReasonCodes = appendReason(assessment.ReasonCodes, "no_evidence")
	} else {
		if diagnostics.Mode != "hybrid" {
			assessment.Gap = maxGap(assessment.Gap, QueryGapSoft)
			assessment.RewriteRecommended = true
			assessment.ReasonCodes = appendReason(assessment.ReasonCodes, "retrieval_degraded")
		}
		if assessment.Signals.HybridEvidenceCount == 0 {
			assessment.Gap = maxGap(assessment.Gap, QueryGapSoft)
			assessment.RewriteRecommended = true
			assessment.ReasonCodes = appendReason(assessment.ReasonCodes, "single_retriever_evidence")
		}
		if assessment.Signals.TopScore < lowEvidenceThreshold {
			assessment.Gap = QueryGapHard
			assessment.RewriteRecommended = true
			assessment.ReasonCodes = appendReason(assessment.ReasonCodes, "low_top_score")
		}
		if assessment.Signals.CrossDocument && assessment.Signals.DistinctSources < 2 {
			assessment.Gap = QueryGapHard
			assessment.RewriteRecommended = true
			assessment.ReasonCodes = appendReason(assessment.ReasonCodes, "cross_document_evidence_gap")
		}
		if hasWeakRankSeparation(hits, assessment.Signals) {
			assessment.RerankRecommended = true
			assessment.ReasonCodes = appendReason(assessment.ReasonCodes, "weak_rank_separation")
		}
		if assessment.Complexity == QueryComplexityComplex && len(hits) > 1 {
			assessment.RerankRecommended = true
		}
	}
	assessment.DeepRecommended = assessment.RewriteRecommended || assessment.RerankRecommended
	if !assessment.DeepRecommended {
		assessment.ReasonCodes = appendReason(assessment.ReasonCodes, "simple_query_high_confidence")
	}
	return assessment
}

// AnalyzeQueryShape performs the pre-retrieval part of the assessment. It is
// safe for routing because it does not infer a retrieval gap before retrieval
// has actually run.
func AnalyzeQueryShape(query string) QueryAssessment {
	signals, score, queryReasons := inspectQuery(query)
	assessment := QueryAssessment{
		Version: QueryAssessmentVersion, Complexity: QueryComplexitySimple,
		ComplexityScore: roundScore(score), Gap: QueryGapNone, Signals: signals,
		ReasonCodes: queryReasons,
	}
	if score >= complexityThreshold {
		assessment.Complexity = QueryComplexityComplex
		assessment.RewriteRecommended = true
	}
	if signals.AmbiguousReference {
		assessment.Gap = QueryGapHard
		assessment.RewriteRecommended = true
	}
	assessment.DeepRecommended = assessment.RewriteRecommended
	return assessment
}

func inspectQuery(query string) (QueryAssessmentSignals, float64, []string) {
	query = strings.TrimSpace(query)
	lower := strings.ToLower(query)
	signals := QueryAssessmentSignals{
		RuneCount: utf8.RuneCountInString(query), ClauseCount: clauseCount(lower),
		Comparison: comparisonPattern.MatchString(lower), CrossDocument: crossDocumentPattern.MatchString(lower),
		Causal: causalPattern.MatchString(lower), Analytical: analyticalPattern.MatchString(lower),
		AmbiguousReference: ambiguousReferencePattern.MatchString(lower),
	}
	score := 0.0
	reasons := make([]string, 0, 6)
	if signals.ClauseCount >= 3 {
		score += 0.35
		reasons = appendReason(reasons, "multi_part_query")
	} else if signals.ClauseCount == 2 {
		score += 0.18
		reasons = appendReason(reasons, "multi_part_query")
	}
	if signals.Comparison {
		score += 0.25
		reasons = appendReason(reasons, "comparison_query")
	}
	if signals.CrossDocument {
		score += 0.25
		reasons = appendReason(reasons, "cross_document_query")
	}
	if signals.Causal {
		score += 0.12
		reasons = appendReason(reasons, "causal_query")
	}
	if signals.Analytical {
		score += 0.18
		reasons = appendReason(reasons, "analytical_query")
	}
	if signals.RuneCount >= 160 {
		score += 0.22
		reasons = appendReason(reasons, "long_query")
	} else if signals.RuneCount >= 80 {
		score += 0.12
		reasons = appendReason(reasons, "long_query")
	}
	if signals.AmbiguousReference {
		reasons = appendReason(reasons, "ambiguous_reference")
	}
	return signals, math.Min(1, score), reasons
}

func clauseCount(query string) int {
	count := 0
	for _, part := range clauseSeparatorPattern.Split(query, -1) {
		if strings.TrimSpace(part) != "" {
			count++
		}
	}
	if count == 0 {
		count = 1
	}
	connectorCount := len(englishConnectorPattern.FindAllStringIndex(query, -1))
	for _, marker := range []string{"并且", "同时", "以及", "然后", "分别", "还要", "再说明"} {
		connectorCount += strings.Count(query, marker)
	}
	if candidate := connectorCount + 1; candidate > count {
		count = candidate
	}
	if count > 6 {
		return 6
	}
	return count
}

func populateRetrievalSignals(signals *QueryAssessmentSignals, hits []SearchHit) {
	if signals == nil || len(hits) == 0 {
		return
	}
	scores := make([]float64, 0, len(hits))
	sources := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		score := hit.Evidence.Score
		scores = append(scores, score)
		if hit.Evidence.SourceID != "" {
			sources[hit.Evidence.SourceID] = struct{}{}
		}
		if strings.EqualFold(hit.Evidence.Retrieval, "dense+bm25") {
			signals.HybridEvidenceCount++
		}
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(scores)))
	signals.TopScore = roundScore(scores[0])
	if len(scores) > 1 {
		signals.ScoreMargin = roundScore(math.Max(0, scores[0]-scores[1]))
	}
	signals.DistinctSources = len(sources)
}

func hasWeakRankSeparation(hits []SearchHit, signals QueryAssessmentSignals) bool {
	return len(hits) > 1 && signals.DistinctSources > 1 && signals.TopScore >= lowEvidenceThreshold && signals.ScoreMargin <= weakRankMargin
}

func appendReason(reasons []string, reason string) []string {
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}

func maxGap(current string, candidate string) string {
	rank := map[string]int{QueryGapNone: 0, QueryGapSoft: 1, QueryGapHard: 2}
	if rank[candidate] > rank[current] {
		return candidate
	}
	return current
}

func roundScore(value float64) float64 {
	return math.Round(value*10000) / 10000
}
