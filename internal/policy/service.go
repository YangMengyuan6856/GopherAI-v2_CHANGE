package policy

import (
	"context"
	"errors"

	"GopherAI/internal/contract"
)

const StrategyControlSchemaVersion = "strategy-control-v1"

type PolicyObserver interface {
	RecordPolicyLoad(source string, result string)
	SetStrategyWeight(policyVersion string, strategy string, weightBasis int)
}

type StrategyControlService struct {
	repository  *CachedPolicyRepository
	registry    *StrategyRegistry
	selector    *WeightedSelector
	environment string
	seed        RoutingPolicyDocument
	observer    PolicyObserver
}

type PolicySnapshot struct {
	LoadedPolicy
	Registry []StrategyMetadata
}

func NewStrategyControlService(repository *CachedPolicyRepository, registry *StrategyRegistry, environment string, seed RoutingPolicyDocument, observer PolicyObserver) (*StrategyControlService, error) {
	if repository == nil || registry == nil {
		return nil, errors.New("strategy control dependencies are required")
	}
	if err := validatePolicyEnvironment(environment); err != nil {
		return nil, err
	}
	if err := seed.Validate(registry); err != nil {
		return nil, err
	}
	selector, err := NewWeightedSelector(registry)
	if err != nil {
		return nil, err
	}
	return &StrategyControlService{repository: repository, registry: registry, selector: selector, environment: environment, seed: seed, observer: observer}, nil
}

func (service *StrategyControlService) Snapshot(ctx context.Context) (PolicySnapshot, error) {
	if service == nil || service.repository == nil {
		return PolicySnapshot{}, errors.New("strategy control service is unavailable")
	}
	loaded, err := service.repository.LoadOrSeed(ctx, service.environment, service.seed)
	if err != nil {
		if service.observer != nil {
			service.observer.RecordPolicyLoad("mysql", "error")
		}
		return PolicySnapshot{}, err
	}
	if err := loaded.Document.Validate(service.registry); err != nil {
		if service.observer != nil {
			service.observer.RecordPolicyLoad(loaded.Source, "invalid")
		}
		return PolicySnapshot{}, err
	}
	if service.observer != nil {
		result := "success"
		if loaded.CacheDegraded {
			result = "cache_degraded"
		}
		service.observer.RecordPolicyLoad(loaded.Source, result)
		for _, rule := range loaded.Document.Rules {
			for _, weighted := range rule.Strategies {
				service.observer.SetStrategyWeight(loaded.Document.Version, weighted.Name, weighted.WeightBasis)
			}
		}
	}
	return PolicySnapshot{LoadedPolicy: loaded, Registry: service.registry.List()}, nil
}

func (service *StrategyControlService) Simulate(ctx context.Context, userID string, intent string, dependencies map[Dependency]bool, budgets contract.ExecutionBudgets) (SelectionResult, PolicySnapshot, error) {
	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		return SelectionResult{}, PolicySnapshot{}, err
	}
	result, err := service.selector.Select(snapshot.Document, SelectionInput{
		TenantID: userID, UserID: userID, Intent: intent, Budgets: budgets, AvailableDependencies: dependencies, AllowShadow: true,
	})
	if err != nil {
		return SelectionResult{}, snapshot, err
	}
	result.PolicySource = snapshot.Source
	return result, snapshot, nil
}
