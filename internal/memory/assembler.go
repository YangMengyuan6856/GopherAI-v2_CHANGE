package memory

import (
	"fmt"
	"sort"
	"strings"
)

const AssemblerVersion = "context-assembler-v2"

type AssembleInput struct {
	SafetyRules     []string
	CurrentQuestion string
	CurrentRunState string
	Summary         StructuredSummary
	ProfileFacts    []ProfileFact
	WorkingMessages []WorkingMessage
	BudgetTokens    int
}

type Assembler struct{}

func NewAssembler() *Assembler { return &Assembler{} }

func (*Assembler) Assemble(input AssembleInput) ContextAssembly {
	budget := boundedTokenBudget(input.BudgetTokens)
	assembly := ContextAssembly{
		Version:          AssemblerVersion,
		BudgetTokens:     budget,
		Included:         make([]ContextItem, 0, len(input.WorkingMessages)+16),
		WorkingAvailable: len(input.WorkingMessages),
		ProfileAvailable: len(input.ProfileFacts),
	}

	required := make([]ContextItem, 0, 8)
	for _, rule := range boundedStrings(input.SafetyRules, 8) {
		required = append(required, contextItem(ContextSafetyRule, "", rule, true))
	}
	if question := boundedText(input.CurrentQuestion, 8000); question != "" {
		required = append(required, contextItem(ContextQuestion, RoleUser, question, true))
	}
	for _, constraint := range boundedStrings(input.Summary.Constraints, 16) {
		required = append(required, contextItem(ContextConstraint, "", constraint, true))
	}
	if runState := boundedText(input.CurrentRunState, 1000); runState != "" {
		required = append(required, contextItem(ContextRunState, "", runState, true))
	}
	for _, item := range required {
		assembly.add(item)
	}

	optional := summaryItems(input.Summary)
	assembly.SummaryAvailable = len(optional)
	profiles := profileItems(input.ProfileFacts)
	working := workingItems(input.WorkingMessages, input.CurrentQuestion)
	for _, item := range append(append(optional, profiles...), working...) {
		assembly.OriginalTokens += item.EstimatedTokens
	}
	for _, item := range required {
		assembly.OriginalTokens += item.EstimatedTokens
	}

	for _, item := range optional {
		if assembly.EstimatedTokens+item.EstimatedTokens > budget {
			assembly.DroppedByBudget++
			continue
		}
		assembly.add(item)
		assembly.SummaryIncluded++
	}
	for _, item := range profiles {
		if assembly.EstimatedTokens+item.EstimatedTokens > budget {
			assembly.DroppedByBudget++
			continue
		}
		assembly.add(item)
		assembly.ProfileIncluded++
	}

	selectedWorking := make([]ContextItem, 0, len(working))
	selectedWorkingTokens := 0
	for index := len(working) - 1; index >= 0; index-- {
		item := working[index]
		if assembly.EstimatedTokens+selectedWorkingTokens+item.EstimatedTokens > budget {
			assembly.DroppedByBudget++
			continue
		}
		selectedWorking = append(selectedWorking, item)
		selectedWorkingTokens += item.EstimatedTokens
	}
	for index := len(selectedWorking) - 1; index >= 0; index-- {
		assembly.add(selectedWorking[index])
		assembly.WorkingIncluded++
	}

	assembly.OverBudget = assembly.EstimatedTokens > budget
	if assembly.OriginalTokens > 0 && assembly.EstimatedTokens < assembly.OriginalTokens {
		assembly.TokenReductionRatio = float64(assembly.OriginalTokens-assembly.EstimatedTokens) / float64(assembly.OriginalTokens)
	}
	return assembly
}

func profileItems(facts []ProfileFact) []ContextItem {
	if len(facts) > 5 {
		facts = facts[:5]
	}
	allowed := map[string]struct{}{
		"os": {}, "go_version": {}, "deployment_mode": {}, "cloud_provider": {}, "redis_version": {}, "mysql_version": {},
	}
	items := make([]ContextItem, 0, len(facts))
	seen := make(map[string]struct{}, len(facts))
	for _, fact := range facts {
		key, value := boundedText(fact.Key, 64), boundedText(fact.Value, 256)
		if _, ok := allowed[key]; !ok || value == "" || fact.Confidence < 0.8 || fact.Confidence > 1 {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, contextItem(ContextProfile, "", fmt.Sprintf("confirmed_environment.%s=%s", key, value), false))
	}
	return items
}

func (assembly *ContextAssembly) add(item ContextItem) {
	assembly.Included = append(assembly.Included, item)
	assembly.EstimatedTokens += item.EstimatedTokens
}

func summaryItems(summary StructuredSummary) []ContextItem {
	items := make([]ContextItem, 0, 64)
	if goal := boundedText(summary.Goal, 1000); goal != "" {
		items = append(items, contextItem(ContextGoal, "", goal, false))
	}
	keys := make([]string, 0, len(summary.ConfirmedFacts))
	for key := range summary.ConfirmedFacts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := boundedText(summary.ConfirmedFacts[key], 2000)
		if value != "" {
			items = append(items, contextItem(ContextFact, "", fmt.Sprintf("%s=%s", boundedText(key, 120), value), false))
		}
	}
	for _, question := range boundedStrings(summary.OpenQuestions, 16) {
		items = append(items, contextItem(ContextOpenQuestion, "", question, false))
	}
	if next := boundedText(summary.NextAction, 1000); next != "" {
		items = append(items, contextItem(ContextNextAction, "", next, false))
	}
	for _, step := range boundedStrings(summary.CompletedSteps, 32) {
		items = append(items, contextItem(ContextCompleted, "", step, false))
	}
	for _, step := range boundedStrings(summary.FailedSteps, 16) {
		items = append(items, contextItem(ContextFailed, "", step, false))
	}
	for _, evidence := range boundedStrings(summary.EvidenceRefs, 32) {
		items = append(items, contextItem(ContextEvidence, "", evidence, false))
	}
	return items
}

func workingItems(messages []WorkingMessage, currentQuestion string) []ContextItem {
	items := make([]ContextItem, 0, len(messages))
	currentQuestion = strings.TrimSpace(currentQuestion)
	currentQuestionIndex := -1
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == RoleUser && strings.TrimSpace(messages[index].Content) == currentQuestion {
			currentQuestionIndex = index
			break
		}
	}
	for index, message := range messages {
		content := boundedText(message.Content, 8000)
		if content == "" {
			continue
		}
		if index == currentQuestionIndex {
			continue
		}
		items = append(items, contextItem(ContextWorking, message.Role, content, false))
	}
	return items
}

func contextItem(kind ContextKind, role Role, content string, required bool) ContextItem {
	return ContextItem{Kind: kind, Role: role, Content: content, Required: required, EstimatedTokens: estimateTokens(content)}
}

func estimateTokens(value string) int {
	runes := len([]rune(strings.TrimSpace(value)))
	if runes == 0 {
		return 0
	}
	return (runes+2)/3 + 4
}

func boundedTokenBudget(value int) int {
	if value <= 0 {
		return DefaultTokenBudget
	}
	if value > MaxTokenBudget {
		return MaxTokenBudget
	}
	return value
}

func boundedStrings(values []string, limit int) []string {
	if len(values) > limit {
		values = values[:limit]
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = boundedText(value, 2000); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func boundedText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}
