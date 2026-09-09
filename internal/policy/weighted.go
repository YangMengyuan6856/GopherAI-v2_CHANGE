package policy

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"GopherAI/internal/contract"
)

const RoutingPolicySchemaVersion = "routing-policy-v1"

type WeightedStrategy struct {
	Name        string `json:"name"`
	WeightBasis int    `json:"weight_basis"`
}

type RoutingRule struct {
	Strategies []WeightedStrategy `json:"strategies"`
	Fallback   string             `json:"fallback"`
}

type RoutingPolicyDocument struct {
	SchemaVersion string                 `json:"schema_version"`
	Version       string                 `json:"version"`
	SeedSalt      string                 `json:"seed_salt"`
	Rules         map[string]RoutingRule `json:"rules"`
}

func (document RoutingPolicyDocument) Validate(registry *StrategyRegistry) error {
	if registry == nil || document.SchemaVersion != RoutingPolicySchemaVersion || strings.TrimSpace(document.Version) == "" || len(document.Version) > 64 || strings.TrimSpace(document.SeedSalt) == "" || len(document.SeedSalt) > 64 {
		return errors.New("routing policy identity is invalid")
	}
	if len(document.Rules) == 0 || len(document.Rules) > 8 {
		return errors.New("routing policy must contain bounded rules")
	}
	for intent, rule := range document.Rules {
		if !strategyIdentifier.MatchString(intent) || len(rule.Strategies) == 0 || len(rule.Strategies) > 8 {
			return fmt.Errorf("routing rule %q is invalid", intent)
		}
		total := 0
		seen := make(map[string]struct{}, len(rule.Strategies))
		for _, weighted := range rule.Strategies {
			metadata, exists := registry.Get(weighted.Name)
			if !exists || metadata.State == StrategyDisabled || !supportsIntent(metadata, intent) || weighted.WeightBasis <= 0 || weighted.WeightBasis > 10_000 {
				return fmt.Errorf("routing rule %q references an ineligible strategy %q", intent, weighted.Name)
			}
			if _, exists := seen[weighted.Name]; exists {
				return fmt.Errorf("routing rule %q repeats strategy %q", intent, weighted.Name)
			}
			seen[weighted.Name] = struct{}{}
			total += weighted.WeightBasis
		}
		if total != 10_000 {
			return fmt.Errorf("routing rule %q weights must total 10000", intent)
		}
		fallback, exists := registry.Get(rule.Fallback)
		if !exists || fallback.State == StrategyDisabled || !supportsIntent(fallback, intent) {
			return fmt.Errorf("routing rule %q has invalid fallback %q", intent, rule.Fallback)
		}
	}
	return nil
}

func DefaultRoutingPolicy() RoutingPolicyDocument {
	single := func(name string) []WeightedStrategy {
		return []WeightedStrategy{{Name: name, WeightBasis: 10_000}}
	}
	return RoutingPolicyDocument{
		SchemaVersion: RoutingPolicySchemaVersion,
		Version:       "routing-policy-v1",
		SeedSalt:      "gopherai-routing-v1",
		Rules: map[string]RoutingRule{
			"legacy":          {Strategies: single(LegacyChatStrategyName), Fallback: "direct_fallback"},
			"general":         {Strategies: single(LegacyChatStrategyName), Fallback: "direct_fallback"},
			"follow_up":       {Strategies: single(LegacyChatStrategyName), Fallback: "direct_fallback"},
			"doc_task":        {Strategies: single(LegacyChatStrategyName), Fallback: "direct_fallback"},
			"tool_task":       {Strategies: single(LegacyChatStrategyName), Fallback: "direct_fallback"},
			"project_qa":      {Strategies: single(RAGFastStrategyName), Fallback: "direct_fallback"},
			"troubleshooting": {Strategies: single("diagnosis_standard"), Fallback: "direct_fallback"},
			"unknown":         {Strategies: single("direct_fallback"), Fallback: "direct_fallback"},
		},
	}
}

type SelectionInput struct {
	TenantID              string
	UserID                string
	Intent                string
	Budgets               contract.ExecutionBudgets
	AvailableDependencies map[Dependency]bool
	AllowShadow           bool
}

type SelectionResult struct {
	Decision           contract.StrategyDecision `json:"decision"`
	Bucket             int                       `json:"bucket"`
	PolicySource       string                    `json:"policy_source,omitempty"`
	FilteredStrategies []string                  `json:"filtered_strategies,omitempty"`
}

type WeightedSelector struct{ registry *StrategyRegistry }

func NewWeightedSelector(registry *StrategyRegistry) (*WeightedSelector, error) {
	if registry == nil {
		return nil, errors.New("strategy registry is required")
	}
	return &WeightedSelector{registry: registry}, nil
}

func (selector *WeightedSelector) Select(document RoutingPolicyDocument, input SelectionInput) (SelectionResult, error) {
	if selector == nil || selector.registry == nil {
		return SelectionResult{}, errors.New("weighted selector is unavailable")
	}
	if err := document.Validate(selector.registry); err != nil {
		return SelectionResult{}, err
	}
	if strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.UserID) == "" {
		return SelectionResult{}, errors.New("selection principal is required")
	}
	intent := strings.TrimSpace(input.Intent)
	rule, exists := document.Rules[intent]
	if !exists {
		intent = "unknown"
		rule, exists = document.Rules[intent]
	}
	if !exists {
		return SelectionResult{}, fmt.Errorf("policy has no rule for intent %q", input.Intent)
	}
	bucket := stableBucket(document.SeedSalt, input.TenantID, input.UserID, intent)
	eligible := make([]WeightedStrategy, 0, len(rule.Strategies))
	filtered := make([]string, 0, len(rule.Strategies))
	for _, weighted := range rule.Strategies {
		metadata, _ := selector.registry.Get(weighted.Name)
		stateEligible := metadata.State == StrategyActive || (input.AllowShadow && metadata.State == StrategyShadow)
		if !stateEligible || !dependenciesAvailable(metadata.Dependencies, input.AvailableDependencies) {
			filtered = append(filtered, weighted.Name)
			continue
		}
		eligible = append(eligible, weighted)
	}
	selectedName := ""
	reason := "stable_weighted_selection"
	if len(eligible) > 0 {
		selectedName = chooseWeighted(eligible, bucket)
		if len(filtered) > 0 {
			reason = "dependency_filtered_selection"
		}
	} else {
		fallback, found := selector.registry.Get(rule.Fallback)
		if !found || fallback.State != StrategyActive || !dependenciesAvailable(fallback.Dependencies, input.AvailableDependencies) {
			return SelectionResult{}, fmt.Errorf("no healthy strategy or fallback for intent %q", intent)
		}
		selectedName = fallback.Name
		reason = "dependency_fallback"
	}
	metadata, _ := selector.registry.Get(selectedName)
	fallbacks := uniqueFallbacks(rule.Fallback, metadata.Fallback)
	return SelectionResult{
		Bucket:             bucket,
		FilteredStrategies: filtered,
		Decision: contract.StrategyDecision{
			StrategyName: selectedName, StrategyVersion: metadata.Version, PolicyVersion: document.Version,
			ReasonCode: reason, ExperimentBucket: fmt.Sprintf("%04d", bucket), Fallbacks: fallbacks,
			Budgets: clampBudgets(input.Budgets, metadata.MaximumBudget),
		},
	}, nil
}

func stableBucket(seed string, tenantID string, userID string, intent string) int {
	sum := sha256.Sum256([]byte(seed + "|" + tenantID + "|" + userID + "|" + intent))
	return int(binary.BigEndian.Uint64(sum[:8]) % 10_000)
}

func dependenciesAvailable(dependencies []Dependency, available map[Dependency]bool) bool {
	for _, dependency := range dependencies {
		if !available[dependency] {
			return false
		}
	}
	return true
}

func chooseWeighted(strategies []WeightedStrategy, bucket int) string {
	total := 0
	for _, strategy := range strategies {
		total += strategy.WeightBasis
	}
	slot := bucket % total
	cumulative := 0
	for _, strategy := range strategies {
		cumulative += strategy.WeightBasis
		if slot < cumulative {
			return strategy.Name
		}
	}
	return strategies[len(strategies)-1].Name
}

func uniqueFallbacks(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func clampBudgets(request contract.ExecutionBudgets, maximum contract.ExecutionBudgets) contract.ExecutionBudgets {
	result := request
	result.MaxAgents = minimumPositive(request.MaxAgents, maximum.MaxAgents)
	result.MaxToolCalls = minimumNonNegative(request.MaxToolCalls, maximum.MaxToolCalls)
	result.MaxIterations = minimumNonNegative(request.MaxIterations, maximum.MaxIterations)
	result.MaxInputTokens = minimumNonNegative(request.MaxInputTokens, maximum.MaxInputTokens)
	result.MaxOutputTokens = minimumNonNegative(request.MaxOutputTokens, maximum.MaxOutputTokens)
	result.MaxCostMicros = minimumNonNegative64(request.MaxCostMicros, maximum.MaxCostMicros)
	result.TotalTimeout = minimumDuration(request.TotalTimeout, maximum.TotalTimeout)
	return result
}

func minimumPositive(left int, right int) int {
	if left <= 0 {
		return right
	}
	if right <= 0 || left < right {
		return left
	}
	return right
}

func minimumNonNegative(left int, right int) int {
	if left < 0 {
		left = 0
	}
	if right > 0 && left > right {
		return right
	}
	return left
}

func minimumNonNegative64(left int64, right int64) int64 {
	if left < 0 {
		left = 0
	}
	if right > 0 && left > right {
		return right
	}
	return left
}

func minimumDuration(left time.Duration, right time.Duration) time.Duration {
	if left <= 0 {
		return right
	}
	if right <= 0 || left < right {
		return left
	}
	return right
}
