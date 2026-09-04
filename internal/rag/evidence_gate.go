package rag

import (
	"GopherAI/internal/contract"
	"strings"
)

const (
	GateReasonSufficient        = "sufficient"
	GateReasonNoEvidence        = "no_evidence"
	GateReasonNoHybridSupport   = "no_cross_retriever_support"
	GateReasonLowConfidence     = "low_top_score"
	DefaultMinimumEvidenceScore = 0.80
)

type EvidenceGateResult struct {
	Accepted            bool     `json:"accepted"`
	ReasonCode          string   `json:"reason_code"`
	TopScore            float64  `json:"top_score"`
	HybridEvidenceCount int      `json:"hybrid_evidence_count"`
	FollowUpQuestions   []string `json:"follow_up_questions,omitempty"`
}

type EvidenceGate struct {
	minimumScore float64
}

func NewEvidenceGate(minimumScore float64) *EvidenceGate {
	if minimumScore <= 0 || minimumScore > 1 {
		minimumScore = DefaultMinimumEvidenceScore
	}
	return &EvidenceGate{minimumScore: minimumScore}
}

func DefaultEvidenceGate() *EvidenceGate {
	return NewEvidenceGate(DefaultMinimumEvidenceScore)
}

func (gate *EvidenceGate) Evaluate(output SearchOutput) EvidenceGateResult {
	result := EvidenceGateResult{}
	for _, hit := range output.Hits {
		if hit.Evidence.Score > result.TopScore {
			result.TopScore = hit.Evidence.Score
		}
		if strings.EqualFold(hit.Evidence.Retrieval, "dense+bm25") {
			result.HybridEvidenceCount++
		}
	}
	if len(output.Hits) == 0 {
		result.ReasonCode = GateReasonNoEvidence
		result.FollowUpQuestions = []string{"请上传包含该问题答案的项目文档，或补充准确的配置名、错误码和相关日志。"}
		return result
	}
	if result.HybridEvidenceCount == 0 || output.Diagnostics.DenseCandidates == 0 || output.Diagnostics.KeywordCandidates == 0 {
		result.ReasonCode = GateReasonNoHybridSupport
		result.FollowUpQuestions = []string{"当前证据只有单路召回，请补充文档中的准确术语、配置名或错误码后再试。"}
		return result
	}
	minimumScore := DefaultMinimumEvidenceScore
	if gate != nil && gate.minimumScore > 0 {
		minimumScore = gate.minimumScore
	}
	if result.TopScore < minimumScore {
		result.ReasonCode = GateReasonLowConfidence
		result.FollowUpQuestions = []string{"当前证据与问题的匹配度不足，请补充更具体的问题描述或相关项目资料。"}
		return result
	}
	result.Accepted = true
	result.ReasonCode = GateReasonSufficient
	return result
}

func EvidenceFromHits(hits []SearchHit) []contract.Evidence {
	evidence := make([]contract.Evidence, 0, len(hits))
	for _, hit := range hits {
		evidence = append(evidence, hit.Evidence)
	}
	return evidence
}
