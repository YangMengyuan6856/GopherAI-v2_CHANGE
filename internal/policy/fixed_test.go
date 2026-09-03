package policy

import (
	"GopherAI/internal/contract"
	"context"
	"testing"
	"time"
)

type staticFlags bool

func (flags staticFlags) Enabled(string) bool { return bool(flags) }

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
