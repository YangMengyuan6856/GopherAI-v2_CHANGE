package policy

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"GopherAI/internal/contract"
)

func allDependenciesAvailable() map[Dependency]bool {
	return map[Dependency]bool{DependencyModel: true, DependencyVector: true, DependencyTool: true, DependencyCaseMemory: true}
}

func TestDefaultRoutingPolicyValidatesAndSelectsDeterministically(t *testing.T) {
	registry := DefaultStrategyRegistry()
	document := DefaultRoutingPolicy()
	if err := document.Validate(registry); err != nil {
		t.Fatal(err)
	}
	selector, _ := NewWeightedSelector(registry)
	input := SelectionInput{TenantID: "tenant", UserID: "alice", Intent: "project_qa", Budgets: testBudget(), AvailableDependencies: allDependenciesAvailable()}
	first, err := selector.Select(document, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := selector.Select(document, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Bucket != second.Bucket || !reflect.DeepEqual(first.Decision, second.Decision) {
		t.Fatalf("selection was not deterministic: %+v %+v", first, second)
	}
	if first.Decision.StrategyName != RAGFastStrategyName || first.Decision.ExperimentBucket == "" {
		t.Fatalf("unexpected project QA selection: %+v", first)
	}
}

func TestWeightedSelectorHonorsStableWeightDistribution(t *testing.T) {
	budget := testBudget()
	registry, err := NewStrategyRegistry(
		StrategyMetadata{Name: "strategy_a", Version: "v1", Intents: []string{"general"}, LatencyTier: "low", CostTier: "low", MaximumBudget: budget, State: StrategyActive, Fallback: "strategy_a", ControlLevel: ControlCanary},
		StrategyMetadata{Name: "strategy_b", Version: "v1", Intents: []string{"general"}, LatencyTier: "low", CostTier: "low", MaximumBudget: budget, State: StrategyActive, Fallback: "strategy_a", ControlLevel: ControlCanary},
	)
	if err != nil {
		t.Fatal(err)
	}
	document := RoutingPolicyDocument{SchemaVersion: RoutingPolicySchemaVersion, Version: "distribution-v1", SeedSalt: "fixed-seed", Rules: map[string]RoutingRule{
		"general": {Strategies: []WeightedStrategy{{Name: "strategy_a", WeightBasis: 7000}, {Name: "strategy_b", WeightBasis: 3000}}, Fallback: "strategy_a"},
	}}
	selector, _ := NewWeightedSelector(registry)
	countA := 0
	for index := 0; index < 2000; index++ {
		result, selectErr := selector.Select(document, SelectionInput{TenantID: "tenant", UserID: fmt.Sprintf("user-%d", index), Intent: "general", Budgets: budget})
		if selectErr != nil {
			t.Fatal(selectErr)
		}
		if result.Decision.StrategyName == "strategy_a" {
			countA++
		}
	}
	if countA < 1300 || countA > 1500 {
		t.Fatalf("70/30 distribution escaped tolerance: strategy_a=%d", countA)
	}
}

func TestWeightedSelectorFiltersDependenciesAndFallsBack(t *testing.T) {
	registry := DefaultStrategyRegistry()
	selector, _ := NewWeightedSelector(registry)
	document := DefaultRoutingPolicy()
	result, err := selector.Select(document, SelectionInput{
		TenantID: "tenant", UserID: "alice", Intent: "project_qa", Budgets: testBudget(),
		AvailableDependencies: map[Dependency]bool{DependencyModel: true, DependencyVector: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.StrategyName != "direct_fallback" || result.Decision.ReasonCode != "dependency_fallback" || len(result.FilteredStrategies) != 1 || result.FilteredStrategies[0] != RAGFastStrategyName {
		t.Fatalf("dependency fallback was not explicit: %+v", result)
	}

	result, err = selector.Select(document, SelectionInput{TenantID: "tenant", UserID: "alice", Intent: "general", Budgets: testBudget()})
	if err != nil || result.Decision.StrategyName != "direct_fallback" {
		t.Fatalf("dependency-free safe fallback was not selected: result=%+v err=%v", result, err)
	}
}

func TestWeightedSelectorClampsServerBudgetsAndUnknownIntent(t *testing.T) {
	registry := DefaultStrategyRegistry()
	selector, _ := NewWeightedSelector(registry)
	requested := contract.ExecutionBudgets{MaxAgents: 99, MaxToolCalls: 99, MaxIterations: 99, MaxInputTokens: 99_999, MaxOutputTokens: 99_999, MaxCostMicros: 9_999_999, TotalTimeout: 10 * time.Minute}
	result, err := selector.Select(DefaultRoutingPolicy(), SelectionInput{TenantID: "tenant", UserID: "alice", Intent: "untrusted_intent", Budgets: requested, AvailableDependencies: allDependenciesAvailable()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.StrategyName != "direct_fallback" || result.Decision.Budgets.MaxAgents != 1 || result.Decision.Budgets.MaxToolCalls != 2 || result.Decision.Budgets.TotalTimeout != 90*time.Second {
		t.Fatalf("unknown intent or budget clamp failed: %+v", result)
	}
}

func TestWeightedSelectorOnlyIncludesShadowStrategiesWhenExplicitlyAllowed(t *testing.T) {
	registry := DefaultStrategyRegistry()
	document := DefaultRoutingPolicy()
	document.Version = "shadow-candidate-v1"
	rule := document.Rules["troubleshooting"]
	rule.Strategies = []WeightedStrategy{{Name: "diagnosis_standard", WeightBasis: 5000}, {Name: "diagnosis_case_based", WeightBasis: 5000}}
	document.Rules["troubleshooting"] = rule
	selector, _ := NewWeightedSelector(registry)
	userID := ""
	for index := 0; index < 100; index++ {
		candidate := fmt.Sprintf("shadow-user-%d", index)
		if stableBucket(document.SeedSalt, "tenant", candidate, "troubleshooting") >= 5000 {
			userID = candidate
			break
		}
	}
	if userID == "" {
		t.Fatal("failed to find deterministic shadow bucket")
	}
	input := SelectionInput{TenantID: "tenant", UserID: userID, Intent: "troubleshooting", Budgets: testBudget(), AvailableDependencies: allDependenciesAvailable()}
	live, err := selector.Select(document, input)
	if err != nil {
		t.Fatal(err)
	}
	if live.Decision.StrategyName != "diagnosis_standard" || len(live.FilteredStrategies) != 1 {
		t.Fatalf("shadow strategy leaked into live selection: %+v", live)
	}
	input.AllowShadow = true
	shadow, err := selector.Select(document, input)
	if err != nil {
		t.Fatal(err)
	}
	if shadow.Decision.StrategyName != "diagnosis_case_based" || len(shadow.FilteredStrategies) != 0 {
		t.Fatalf("explicit shadow simulation did not evaluate candidate: %+v", shadow)
	}
}

func TestRoutingPolicyRejectsDisabledOrInvalidWeights(t *testing.T) {
	registry := DefaultStrategyRegistry()
	document := DefaultRoutingPolicy()
	rule := document.Rules["troubleshooting"]
	rule.Strategies = []WeightedStrategy{{Name: "diagnosis_collaborative", WeightBasis: 10_000}}
	document.Rules["troubleshooting"] = rule
	if err := document.Validate(registry); err == nil {
		t.Fatal("disabled strategy was accepted")
	}
	document = DefaultRoutingPolicy()
	rule = document.Rules["general"]
	rule.Strategies[0].WeightBasis = 9999
	document.Rules["general"] = rule
	if err := document.Validate(registry); err == nil {
		t.Fatal("invalid weight total was accepted")
	}
}
