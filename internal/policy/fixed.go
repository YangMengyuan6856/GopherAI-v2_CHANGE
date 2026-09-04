package policy

import (
	"GopherAI/internal/contract"
	"GopherAI/internal/platform/feature"
	"context"
)

const (
	LegacyChatStrategyName    = "legacy_chat"
	LegacyChatStrategyVersion = "legacy-v0"
	PolicyVersionV0           = "policy-v0"
	LegacyBypassPolicyVersion = "legacy-bypass-v0"
	RAGFastStrategyName       = "rag_fast"
	RAGFastStrategyVersion    = "rag-fast-v1"
	RAGFastPolicyVersion      = "policy-rag-fast-v1"
)

type FixedSelector struct {
	flags feature.Provider
}

var _ interface {
	Select(context.Context, contract.RequestContext, contract.IntentResult) (contract.StrategyDecision, error)
} = (*FixedSelector)(nil)

func NewFixedSelector(flags feature.Provider) *FixedSelector {
	return &FixedSelector{flags: flags}
}

func (selector *FixedSelector) Select(_ context.Context, request contract.RequestContext, _ contract.IntentResult) (contract.StrategyDecision, error) {
	policyVersion := PolicyVersionV0
	reasonCode := "m2_fixed_policy"
	if selector.flags != nil && !selector.flags.Enabled(feature.DevSupportEnabled) {
		policyVersion = LegacyBypassPolicyVersion
		reasonCode = "devsupport_disabled_legacy_bypass"
	}
	if request.KnowledgeRequired && policyVersion != LegacyBypassPolicyVersion {
		if selector.flags != nil && !selector.flags.Enabled(feature.RAGFastEnabled) {
			return contract.StrategyDecision{}, contract.NewDomainError("RAG_FAST_DISABLED", contract.ErrorDependencyUnavailable, "知识库回答当前未启用", false, nil)
		}
		return contract.StrategyDecision{
			StrategyName: RAGFastStrategyName, StrategyVersion: RAGFastStrategyVersion,
			PolicyVersion: RAGFastPolicyVersion, ReasonCode: "explicit_knowledge_request",
			Budgets: request.Budgets,
		}, nil
	}
	return contract.StrategyDecision{
		StrategyName:    LegacyChatStrategyName,
		StrategyVersion: LegacyChatStrategyVersion,
		PolicyVersion:   policyVersion,
		ReasonCode:      reasonCode,
		Fallbacks:       []string{LegacyChatStrategyName},
		Budgets:         request.Budgets,
	}, nil
}
