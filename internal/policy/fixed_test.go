package policy

import (
	"GopherAI/internal/contract"
	"GopherAI/internal/platform/feature"
	"context"
	"errors"
	"testing"
	"time"
)

type staticFlags bool

func (flags staticFlags) Enabled(string) bool { return bool(flags) }

type namedFlags map[string]bool

func (flags namedFlags) Enabled(name string) bool { return flags[name] }

func TestFixedSelectorPinsPolicyForRequest(t *testing.T) {
	request := contract.RequestContext{Budgets: contract.ExecutionBudgets{MaxAgents: 1, TotalTimeout: time.Minute}}
	decision, err := NewFixedSelector(staticFlags(true)).Select(context.Background(), request, contract.IntentResult{})
	if err != nil {
		t.Fatal(err)
	}
	if decision.StrategyName != LegacyChatStrategyName || decision.PolicyVersion != PolicyVersionV0 {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if err := decision.Validate(); err != nil {
		t.Fatalf("decision contract is invalid: %v", err)
	}
}

func TestFixedSelectorUsesLegacyBypassWhenFeatureDisabled(t *testing.T) {
	request := contract.RequestContext{Budgets: contract.ExecutionBudgets{MaxAgents: 1, TotalTimeout: time.Minute}}
	decision, err := NewFixedSelector(staticFlags(false)).Select(context.Background(), request, contract.IntentResult{})
	if err != nil {
		t.Fatal(err)
	}
	if decision.PolicyVersion != LegacyBypassPolicyVersion || decision.ReasonCode != "devsupport_disabled_legacy_bypass" {
		t.Fatalf("unexpected bypass decision: %#v", decision)
	}
}

func TestFixedSelectorChoosesRAGFastOnlyForExplicitKnowledgeRequest(t *testing.T) {
	request := contract.RequestContext{
		KnowledgeRequired: true,
		Budgets:           contract.ExecutionBudgets{MaxAgents: 1, TotalTimeout: time.Minute},
	}
	decision, err := NewFixedSelector(namedFlags{feature.DevSupportEnabled: true, feature.RAGFastEnabled: true}).Select(context.Background(), request, contract.IntentResult{})
	if err != nil {
		t.Fatal(err)
	}
	if decision.StrategyName != RAGFastStrategyName || decision.PolicyVersion != RAGFastPolicyVersion || decision.ReasonCode != "explicit_knowledge_request" {
		t.Fatalf("unexpected rag_fast decision: %+v", decision)
	}
}

func TestFixedSelectorRejectsExplicitKnowledgeRequestWhenRAGFlagIsOff(t *testing.T) {
	request := contract.RequestContext{
		KnowledgeRequired: true,
		Budgets:           contract.ExecutionBudgets{MaxAgents: 1, TotalTimeout: time.Minute},
	}
	_, err := NewFixedSelector(namedFlags{feature.DevSupportEnabled: true, feature.RAGFastEnabled: false}).Select(context.Background(), request, contract.IntentResult{})
	var domainError *contract.DomainError
	if !errors.As(err, &domainError) || domainError.Code != "RAG_FAST_DISABLED" {
		t.Fatalf("explicit request must not silently fall back to an ungrounded answer: %v", err)
	}
}
