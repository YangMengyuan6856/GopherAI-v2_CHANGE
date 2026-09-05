package policy

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"GopherAI/internal/contract"
)

type Dependency string
type StrategyState string
type ControlLevel string

const (
	DependencyModel      Dependency = "model"
	DependencyVector     Dependency = "vector"
	DependencyTool       Dependency = "tool"
	DependencyCaseMemory Dependency = "case_memory"

	StrategyActive   StrategyState = "active"
	StrategyShadow   StrategyState = "shadow"
	StrategyDisabled StrategyState = "disabled"

	ControlNone   ControlLevel = "none"
	ControlShadow ControlLevel = "shadow"
	ControlCanary ControlLevel = "canary"

	DiagnosisStandardStrategyName      = "diagnosis_standard"
	DiagnosisCaseBasedStrategyName     = "diagnosis_case_based"
	DiagnosisCollaborativeStrategyName = "diagnosis_collaborative"
)

var strategyIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

type StrategyMetadata struct {
	Name          string                    `json:"name"`
	Version       string                    `json:"version"`
	Intents       []string                  `json:"intents"`
	LatencyTier   string                    `json:"latency_tier"`
	CostTier      string                    `json:"cost_tier"`
	Dependencies  []Dependency              `json:"dependencies"`
	MaximumBudget contract.ExecutionBudgets `json:"maximum_budget"`
	State         StrategyState             `json:"state"`
	Fallback      string                    `json:"fallback,omitempty"`
	ControlLevel  ControlLevel              `json:"control_level"`
}

type StrategyRegistry struct{ items map[string]StrategyMetadata }

func NewStrategyRegistry(items ...StrategyMetadata) (*StrategyRegistry, error) {
	registry := &StrategyRegistry{items: make(map[string]StrategyMetadata, len(items))}
	for _, item := range items {
		if err := validateMetadata(item); err != nil {
			return nil, err
		}
		if _, exists := registry.items[item.Name]; exists {
			return nil, fmt.Errorf("duplicate strategy %q", item.Name)
		}
		item.Intents = append([]string{}, item.Intents...)
		item.Dependencies = append([]Dependency{}, item.Dependencies...)
		registry.items[item.Name] = item
	}
	for _, item := range registry.items {
		if item.Fallback != "" {
			fallback, exists := registry.items[item.Fallback]
			if !exists {
				return nil, fmt.Errorf("strategy %q has unknown fallback %q", item.Name, item.Fallback)
			}
			for _, intent := range item.Intents {
				if !supportsIntent(fallback, intent) {
					return nil, fmt.Errorf("strategy %q fallback %q does not support intent %q", item.Name, item.Fallback, intent)
				}
			}
		}
	}
	return registry, nil
}

func (registry *StrategyRegistry) Get(name string) (StrategyMetadata, bool) {
	if registry == nil {
		return StrategyMetadata{}, false
	}
	item, exists := registry.items[name]
	if !exists {
		return StrategyMetadata{}, false
	}
	item.Intents = append([]string{}, item.Intents...)
	item.Dependencies = append([]Dependency{}, item.Dependencies...)
	return item, true
}

func (registry *StrategyRegistry) List() []StrategyMetadata {
	if registry == nil {
		return []StrategyMetadata{}
	}
	result := make([]StrategyMetadata, 0, len(registry.items))
	for name := range registry.items {
		item, _ := registry.Get(name)
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func validateMetadata(item StrategyMetadata) error {
	if !strategyIdentifier.MatchString(item.Name) || strings.TrimSpace(item.Version) == "" || len(item.Version) > 64 {
		return errors.New("strategy name and version are required")
	}
	if len(item.Intents) == 0 || len(item.Intents) > 8 {
		return fmt.Errorf("strategy %q must declare bounded intents", item.Name)
	}
	seen := make(map[string]struct{}, len(item.Intents))
	for _, intent := range item.Intents {
		if !strategyIdentifier.MatchString(intent) {
			return fmt.Errorf("strategy %q has invalid intent %q", item.Name, intent)
		}
		if _, exists := seen[intent]; exists {
			return fmt.Errorf("strategy %q repeats intent %q", item.Name, intent)
		}
		seen[intent] = struct{}{}
	}
	if item.LatencyTier != "low" && item.LatencyTier != "medium" && item.LatencyTier != "high" {
		return fmt.Errorf("strategy %q has invalid latency tier", item.Name)
	}
	if item.CostTier != "low" && item.CostTier != "medium" && item.CostTier != "high" {
		return fmt.Errorf("strategy %q has invalid cost tier", item.Name)
	}
	if item.State != StrategyActive && item.State != StrategyShadow && item.State != StrategyDisabled {
		return fmt.Errorf("strategy %q has invalid state", item.Name)
	}
	if item.ControlLevel != ControlNone && item.ControlLevel != ControlShadow && item.ControlLevel != ControlCanary {
		return fmt.Errorf("strategy %q has invalid control level", item.Name)
	}
	if err := item.MaximumBudget.Validate(); err != nil {
		return fmt.Errorf("strategy %q maximum budget: %w", item.Name, err)
	}
	if item.MaximumBudget.MaxToolCalls < 1 || item.MaximumBudget.MaxIterations < 1 || item.MaximumBudget.MaxInputTokens < 1 || item.MaximumBudget.MaxOutputTokens < 1 || item.MaximumBudget.MaxCostMicros < 1 {
		return fmt.Errorf("strategy %q maximum budget must bound every resource", item.Name)
	}
	seenDependencies := make(map[Dependency]struct{}, len(item.Dependencies))
	for _, dependency := range item.Dependencies {
		switch dependency {
		case DependencyModel, DependencyVector, DependencyTool, DependencyCaseMemory:
		default:
			return fmt.Errorf("strategy %q has unknown dependency %q", item.Name, dependency)
		}
		if _, exists := seenDependencies[dependency]; exists {
			return fmt.Errorf("strategy %q repeats dependency %q", item.Name, dependency)
		}
		seenDependencies[dependency] = struct{}{}
	}
	return nil
}

func DefaultStrategyRegistry() *StrategyRegistry {
	standard := contract.ExecutionBudgets{MaxAgents: 1, MaxToolCalls: 2, MaxIterations: 6, MaxInputTokens: 16_000, MaxOutputTokens: 4_000, MaxCostMicros: 500_000, TotalTimeout: 90 * time.Second}
	collaborative := standard
	collaborative.MaxAgents, collaborative.MaxToolCalls, collaborative.MaxIterations = 2, 4, 10
	collaborative.TotalTimeout = 120 * time.Second
	registry, err := NewStrategyRegistry(
		StrategyMetadata{Name: LegacyChatStrategyName, Version: LegacyChatStrategyVersion, Intents: []string{"legacy", "general", "follow_up", "doc_task", "tool_task"}, LatencyTier: "medium", CostTier: "medium", Dependencies: []Dependency{DependencyModel}, MaximumBudget: standard, State: StrategyActive, Fallback: "direct_fallback", ControlLevel: ControlNone},
		StrategyMetadata{Name: RAGFastStrategyName, Version: RAGFastStrategyVersion, Intents: []string{"project_qa"}, LatencyTier: "medium", CostTier: "medium", Dependencies: []Dependency{DependencyModel, DependencyVector}, MaximumBudget: standard, State: StrategyActive, Fallback: "direct_fallback", ControlLevel: ControlNone},
		StrategyMetadata{Name: "rag_deep", Version: "rag-deep-v1", Intents: []string{"project_qa"}, LatencyTier: "high", CostTier: "high", Dependencies: []Dependency{DependencyModel, DependencyVector}, MaximumBudget: standard, State: StrategyShadow, Fallback: RAGFastStrategyName, ControlLevel: ControlShadow},
		StrategyMetadata{Name: DiagnosisStandardStrategyName, Version: "diagnosis-standard-v1", Intents: []string{"troubleshooting"}, LatencyTier: "medium", CostTier: "medium", Dependencies: []Dependency{DependencyModel, DependencyTool}, MaximumBudget: standard, State: StrategyActive, Fallback: "direct_fallback", ControlLevel: ControlNone},
		StrategyMetadata{Name: DiagnosisCaseBasedStrategyName, Version: "diagnosis-case-v1", Intents: []string{"troubleshooting"}, LatencyTier: "medium", CostTier: "medium", Dependencies: []Dependency{DependencyModel, DependencyTool, DependencyCaseMemory}, MaximumBudget: standard, State: StrategyShadow, Fallback: DiagnosisStandardStrategyName, ControlLevel: ControlShadow},
		StrategyMetadata{Name: DiagnosisCollaborativeStrategyName, Version: "diagnosis-collaborative-v1", Intents: []string{"troubleshooting"}, LatencyTier: "high", CostTier: "high", Dependencies: []Dependency{DependencyModel, DependencyVector, DependencyTool, DependencyCaseMemory}, MaximumBudget: collaborative, State: StrategyDisabled, Fallback: DiagnosisStandardStrategyName, ControlLevel: ControlShadow},
		StrategyMetadata{Name: "direct_fallback", Version: "direct-fallback-v1", Intents: []string{"legacy", "general", "follow_up", "doc_task", "tool_task", "project_qa", "troubleshooting", "unknown"}, LatencyTier: "low", CostTier: "low", MaximumBudget: standard, State: StrategyActive, ControlLevel: ControlNone},
	)
	if err != nil {
		panic(err)
	}
	return registry
}

func supportsIntent(metadata StrategyMetadata, intent string) bool {
	for _, candidate := range metadata.Intents {
		if candidate == intent {
			return true
		}
	}
	return false
}
