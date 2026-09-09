package diagnostic

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

const (
	CaseBasedStrategyName      = "diagnosis_case_based"
	CaseBasedStrategyVersion   = "diagnosis-case-v1"
	CaseBasedPolicyVersion     = "case-strategy-policy-v1"
	CaseStrategySchemaVersion  = "case-strategy-shadow-v1"
	CaseStrengthStrong         = "strong"
	CaseStrengthWeak           = "weak"
	CaseStrengthNone           = "none"
	caseStrongThreshold        = 0.85
	defaultCaseStrategyTimeout = 1200 * time.Millisecond
)

type CasePriorityRecommendation struct {
	HypothesisID         string  `json:"hypothesis_id"`
	HistoricalIncidentID string  `json:"historical_incident_id"`
	HistoricalRootCause  string  `json:"historical_root_cause"`
	HistoricalResolution string  `json:"historical_resolution"`
	Similarity           float64 `json:"similarity"`
	AdvisoryOnly         bool    `json:"advisory_only"`
}

type CaseStrategyResult struct {
	SchemaVersion          string                      `json:"schema_version"`
	Strategy               string                      `json:"strategy"`
	StrategyVersion        string                      `json:"strategy_version"`
	PolicyVersion          string                      `json:"policy_version"`
	Baseline               Result                      `json:"baseline"`
	CaseMemoryStatus       string                      `json:"case_memory_status"`
	CaseStrength           string                      `json:"case_strength"`
	Cases                  []SimilarIncident           `json:"cases"`
	PriorityRecommendation *CasePriorityRecommendation `json:"priority_recommendation,omitempty"`
	FallbackStrategy       string                      `json:"fallback_strategy,omitempty"`
	ReasonCode             string                      `json:"reason_code"`
	AffectsLiveTraffic     bool                        `json:"affects_live_traffic"`
	Mode                   string                      `json:"mode"`
}

type CaseBasedStrategy struct {
	base      Analyzer
	retriever CaseRetriever
	timeout   time.Duration
}

func NewCaseBasedStrategy(base Analyzer, retriever CaseRetriever, timeout time.Duration) (*CaseBasedStrategy, error) {
	if base == nil || retriever == nil {
		return nil, errors.New("case-based strategy dependencies are required")
	}
	if timeout <= 0 {
		timeout = defaultCaseStrategyTimeout
	}
	return &CaseBasedStrategy{base: base, retriever: retriever, timeout: timeout}, nil
}

func (strategy *CaseBasedStrategy) Analyze(ctx context.Context, tenantID string, userID string, message string) (CaseStrategyResult, error) {
	if strategy == nil || strategy.base == nil || strategy.retriever == nil {
		return CaseStrategyResult{}, errors.New("case-based strategy is unavailable")
	}
	extracted, baseline, err := strategy.base.AnalyzeContext(ctx, message)
	if err != nil {
		return CaseStrategyResult{}, err
	}
	result := CaseStrategyResult{
		SchemaVersion: CaseStrategySchemaVersion, Strategy: CaseBasedStrategyName, StrategyVersion: CaseBasedStrategyVersion,
		PolicyVersion: CaseBasedPolicyVersion, Baseline: baseline, CaseMemoryStatus: CaseMemoryNoMatch,
		CaseStrength: CaseStrengthNone, Cases: []SimilarIncident{}, ReasonCode: "case_no_match",
		AffectsLiveTraffic: false, Mode: "shadow_only",
	}
	recallContext, cancel := context.WithTimeout(ctx, strategy.timeout)
	defer cancel()
	cases, recallErr := strategy.retriever.RetrieveSimilar(recallContext, strings.TrimSpace(tenantID), strings.TrimSpace(userID), extracted, 3)
	if recallErr != nil {
		if ctx.Err() != nil {
			return CaseStrategyResult{}, ctx.Err()
		}
		result.CaseMemoryStatus = CaseMemoryUnavailable
		result.FallbackStrategy = StrategyName
		result.ReasonCode = "case_recall_unavailable"
		return result, nil
	}
	if len(cases) == 0 {
		return result, nil
	}
	sort.SliceStable(cases, func(i, j int) bool { return cases[i].Score > cases[j].Score })
	if len(cases) > 3 {
		cases = cases[:3]
	}
	validationCandidate := baseline
	validationCandidate.CaseMemoryStatus = CaseMemoryHit
	validationCandidate.CaseMemoryPolicy = CaseMemoryPolicyV1
	validationCandidate.SimilarIncidents = cases
	if validationCandidate.Validate() != nil {
		result.CaseMemoryStatus = CaseMemoryUnavailable
		result.FallbackStrategy = StrategyName
		result.ReasonCode = "case_payload_invalid"
		return result, nil
	}
	result.CaseMemoryStatus = CaseMemoryHit
	result.Cases = append([]SimilarIncident{}, cases...)
	result.CaseStrength = CaseStrengthWeak
	result.ReasonCode = "case_match_advisory_only"
	if cases[0].Score < caseStrongThreshold {
		return result, nil
	}
	hypothesisID := matchingHypothesisID(baseline.Hypotheses, cases[0].MatchedErrorSignatures)
	if hypothesisID == "" {
		result.ReasonCode = "strong_case_without_baseline_hypothesis"
		return result, nil
	}
	result.CaseStrength = CaseStrengthStrong
	result.ReasonCode = "strong_case_prioritization_candidate"
	result.PriorityRecommendation = &CasePriorityRecommendation{
		HypothesisID: hypothesisID, HistoricalIncidentID: cases[0].IncidentID,
		HistoricalRootCause: cases[0].RootCause, HistoricalResolution: cases[0].Resolution,
		Similarity: cases[0].Score, AdvisoryOnly: true,
	}
	return result, nil
}

func matchingHypothesisID(hypotheses []Hypothesis, signatures []string) string {
	for _, hypothesis := range hypotheses {
		for _, evidence := range hypothesis.Evidence {
			evidenceID := strings.ToLower(strings.TrimSpace(evidence.ID))
			for _, signature := range signatures {
				if normalized := strings.ToLower(strings.TrimSpace(signature)); normalized != "" && strings.Contains(evidenceID, normalized) {
					return hypothesis.ID
				}
			}
		}
	}
	return ""
}
