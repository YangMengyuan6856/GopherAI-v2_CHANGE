package policy

import (
	"testing"
	"time"

	"GopherAI/internal/contract"
)

func testBudget() contract.ExecutionBudgets {
	return contract.ExecutionBudgets{MaxAgents: 1, MaxToolCalls: 2, MaxIterations: 4, MaxInputTokens: 1000, MaxOutputTokens: 500, MaxCostMicros: 100, TotalTimeout: time.Minute}
}

func TestDefaultStrategyRegistryIsValidAndCopySafe(t *testing.T) {
	registry := DefaultStrategyRegistry()
	items := registry.List()
	if len(items) != 7 {
		t.Fatalf("expected seven registered strategies, got %d", len(items))
	}
	for index := 1; index < len(items); index++ {
		if items[index-1].Name >= items[index].Name {
			t.Fatalf("registry is not sorted: %+v", items)
		}
	}
	item, ok := registry.Get(LegacyChatStrategyName)
	if !ok {
		t.Fatal("legacy strategy is missing")
	}
	item.Intents[0] = "tampered"
	unchanged, _ := registry.Get(LegacyChatStrategyName)
	if unchanged.Intents[0] == "tampered" {
		t.Fatal("registry returned mutable internal metadata")
	}
	fallback, ok := registry.Get("direct_fallback")
	if !ok || fallback.Dependencies == nil {
		t.Fatal("dependency-free strategy must serialize as an empty array, not null")
	}
}

func TestStrategyRegistryRejectsInvalidMetadataAndFallback(t *testing.T) {
	valid := StrategyMetadata{Name: "valid_strategy", Version: "v1", Intents: []string{"general"}, LatencyTier: "low", CostTier: "low", MaximumBudget: testBudget(), State: StrategyActive, ControlLevel: ControlNone}
	if _, err := NewStrategyRegistry(valid, valid); err == nil {
		t.Fatal("duplicate strategy was accepted")
	}
	invalidDependency := valid
	invalidDependency.Name = "bad_dependency"
	invalidDependency.Dependencies = []Dependency{"shell"}
	if _, err := NewStrategyRegistry(invalidDependency); err == nil {
		t.Fatal("unknown dependency was accepted")
	}
	repeatedDependency := valid
	repeatedDependency.Name = "repeated_dependency"
	repeatedDependency.Dependencies = []Dependency{DependencyModel, DependencyModel}
	if _, err := NewStrategyRegistry(repeatedDependency); err == nil {
		t.Fatal("repeated dependency was accepted")
	}
	unknownFallback := valid
	unknownFallback.Fallback = "missing_strategy"
	if _, err := NewStrategyRegistry(unknownFallback); err == nil {
		t.Fatal("unknown fallback was accepted")
	}
}
