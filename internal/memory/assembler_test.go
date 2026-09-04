package memory

import (
	"strings"
	"testing"
)

func TestAssemblerPreservesRequiredContextAndKeepsNewestWorkingMessages(t *testing.T) {
	messages := []WorkingMessage{
		{Role: RoleUser, Content: strings.Repeat("旧问题", 80)},
		{Role: RoleAssistant, Content: strings.Repeat("旧回答", 80)},
		{Role: RoleUser, Content: "当前明确问题"},
	}
	result := NewAssembler().Assemble(AssembleInput{
		SafetyRules: []string{"禁止执行写操作"}, CurrentQuestion: "当前明确问题", CurrentRunState: "WAITING_USER v5",
		Summary:         StructuredSummary{Constraints: []string{"只能只读验证"}, ConfirmedFacts: map[string]string{"redis": "启用了密码"}},
		WorkingMessages: messages, BudgetTokens: 80,
	})
	for _, expected := range []ContextKind{ContextSafetyRule, ContextQuestion, ContextConstraint, ContextRunState} {
		if !hasContextKind(result.Included, expected) {
			t.Fatalf("required context %s was dropped: %+v", expected, result)
		}
	}
	if result.DroppedByBudget == 0 || result.OriginalTokens <= result.EstimatedTokens {
		t.Fatalf("budget did not compress optional context: %+v", result)
	}
	if result.WorkingAvailable != 3 {
		t.Fatalf("working message count changed: %+v", result)
	}
}

func TestAssemblerIsDeterministicAndCapsBudget(t *testing.T) {
	input := AssembleInput{
		CurrentQuestion: "why", BudgetTokens: MaxTokenBudget + 100,
		Summary: StructuredSummary{ConfirmedFacts: map[string]string{"z": "last", "a": "first"}},
	}
	first := NewAssembler().Assemble(input)
	second := NewAssembler().Assemble(input)
	if first.BudgetTokens != MaxTokenBudget || len(first.Included) != len(second.Included) {
		t.Fatalf("unexpected assembly: first=%+v second=%+v", first, second)
	}
	for index := range first.Included {
		if first.Included[index] != second.Included[index] {
			t.Fatalf("assembly is not deterministic: first=%+v second=%+v", first.Included, second.Included)
		}
	}
}

func hasContextKind(items []ContextItem, kind ContextKind) bool {
	for _, item := range items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}
