package policy

import (
	"context"
	"testing"
)

type fakePolicyObserver struct {
	loads   []string
	weights map[string]int
}

func (observer *fakePolicyObserver) RecordPolicyLoad(source string, result string) {
	observer.loads = append(observer.loads, source+":"+result)
}

func (observer *fakePolicyObserver) SetStrategyWeight(policyVersion string, strategy string, weightBasis int) {
	if observer.weights == nil {
		observer.weights = make(map[string]int)
	}
	observer.weights[policyVersion+":"+strategy] = weightBasis
}

func TestStrategyControlServiceExposesShadowSnapshotAndSimulation(t *testing.T) {
	record, err := testPolicyRecord(DefaultPolicyEnvironment, DefaultRoutingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	repository := NewCachedPolicyRepository(&fakePolicyAuthority{record: record}, &fakePolicyCache{loadErr: ErrPolicyCacheMiss})
	observer := new(fakePolicyObserver)
	service, err := NewStrategyControlService(repository, DefaultStrategyRegistry(), DefaultPolicyEnvironment, DefaultRoutingPolicy(), observer)
	if err != nil {
		t.Fatal(err)
	}
	result, snapshot, err := service.Simulate(context.Background(), "alice", "troubleshooting", allDependenciesAvailable(), testBudget())
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.StrategyName != "diagnosis_standard" || result.PolicySource != "mysql" || len(snapshot.Registry) != 7 || len(observer.loads) != 1 || len(observer.weights) != 4 {
		t.Fatalf("unexpected strategy control result: result=%+v snapshot=%+v observer=%+v", result, snapshot, observer)
	}
}

func TestStrategyControlServiceRejectsPersistedPolicyOutsideRegistry(t *testing.T) {
	document := DefaultRoutingPolicy()
	rule := document.Rules["general"]
	rule.Strategies[0].WeightBasis = 9999
	document.Rules["general"] = rule
	record, err := testPolicyRecord(DefaultPolicyEnvironment, document)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewStrategyControlService(NewCachedPolicyRepository(&fakePolicyAuthority{record: record}, nil), DefaultStrategyRegistry(), DefaultPolicyEnvironment, DefaultRoutingPolicy(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Snapshot(context.Background()); err == nil {
		t.Fatal("persisted invalid policy escaped registry validation")
	}
}
