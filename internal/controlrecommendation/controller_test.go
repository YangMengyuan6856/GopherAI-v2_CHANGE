package controlrecommendation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/observability"
	"GopherAI/internal/policy"
	"GopherAI/model"
)

type fakePolicyReader struct{ snapshot policy.PolicySnapshot }

func (reader *fakePolicyReader) Snapshot(context.Context) (policy.PolicySnapshot, error) {
	return reader.snapshot, nil
}

type fakeEvaluationReader struct{ gate EvaluationGate }

func (reader *fakeEvaluationReader) Load() (EvaluationGate, error) { return reader.gate, nil }

type memoryRecommendationRepository struct{ rows []model.ControlRecommendation }

func (repository *memoryRecommendationRepository) Create(_ context.Context, record model.ControlRecommendation) (bool, error) {
	if err := validateRecord(record); err != nil {
		return false, err
	}
	for _, row := range repository.rows {
		if row.RecommendationID == record.RecommendationID {
			return false, nil
		}
	}
	repository.rows = append(repository.rows, record)
	return true, nil
}

func (repository *memoryRecommendationRepository) Audit(context.Context, int) (int64, int64, []model.ControlRecommendation, error) {
	var recommended, blocked int64
	for _, row := range repository.rows {
		if row.Status == StatusRecommended {
			recommended++
		} else if row.Status == StatusBlocked {
			blocked++
		}
	}
	return recommended, blocked, append([]model.ControlRecommendation(nil), repository.rows...), nil
}

func TestAcceptanceProvesBaselineGuardCandidateAndNoActivation(t *testing.T) {
	now := time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)
	policyReader := &fakePolicyReader{snapshot: activePolicySnapshot()}
	repository := &memoryRecommendationRepository{}
	controller, err := NewController(policyReader, &fakeEvaluationReader{gate: unreviewedGate()}, repository, policy.DefaultStrategyRegistry())
	if err != nil {
		t.Fatal(err)
	}
	controller.clock = func() time.Time { return now }
	result, err := controller.Acceptance(context.Background(), AcceptanceScenario)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Simulation || !result.ActivePolicyUnchanged || result.ActivePolicyBefore != result.ActivePolicyAfter || len(repository.rows) != 2 {
		t.Fatalf("acceptance did not preserve active policy: %+v rows=%d", result, len(repository.rows))
	}
	if result.BaselineGuard.Status != StatusBlocked || result.BaselineGuard.ReasonCode != ReasonBaselineNotEligible || result.BaselineGuard.BaselineEligible {
		t.Fatalf("unreviewed baseline was not blocked: %+v", result.BaselineGuard)
	}
	candidate := result.EligibleFixture
	if candidate.Status != StatusRecommended || candidate.ReasonCode != ReasonCandidateCreated || candidate.Applied || !candidate.BaselineEligible ||
		candidate.BeforeWeightBasis != 10_000 || candidate.ProposedWeightBasis != 9_000 || candidate.WeightDeltaBasis != -1_000 ||
		candidate.FallbackStrategy != "direct_fallback" || candidate.FallbackBeforeBasis != 0 || candidate.FallbackProposedBasis != 1_000 {
		t.Fatalf("eligible candidate is wrong: %+v", candidate)
	}
	var document policy.RoutingPolicyDocument
	if err := json.Unmarshal([]byte(repository.rows[1].CandidatePolicyJSON), &document); err != nil || document.Validate(policy.DefaultStrategyRegistry()) != nil {
		t.Fatalf("candidate policy is not independently valid: %v", err)
	}
	if len(candidate.CandidatePolicySHA256) != 64 || len(candidate.EvidenceSHA256) != 64 || candidate.ParentPolicyVersion != "routing-policy-v1" {
		t.Fatalf("candidate lineage is incomplete: %+v", candidate)
	}
}

func TestProductionReconcileWritesBlockedAuditOnceWhenBaselineIsUnreviewed(t *testing.T) {
	now := time.Date(2026, 9, 6, 4, 5, 0, 0, time.UTC)
	snapshot, series, err := controllerAcceptanceFixture(now)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Simulation, snapshot.Source, snapshot.Series = false, "mysql_metric_window_snapshots", []observability.ProductionMetricAnalysis{series}
	repository := &memoryRecommendationRepository{}
	controller, _ := NewController(&fakePolicyReader{snapshot: activePolicySnapshot()}, &fakeEvaluationReader{gate: unreviewedGate()}, repository, policy.DefaultStrategyRegistry())
	controller.clock = func() time.Time { return now }
	first, err := controller.Reconcile(context.Background(), snapshot)
	if err != nil || first.Examined != 1 || first.Blocked != 1 || first.Recommended != 0 {
		t.Fatalf("unexpected first reconcile: %+v err=%v", first, err)
	}
	second, err := controller.Reconcile(context.Background(), snapshot)
	if err != nil || second.Duplicate != 1 || len(repository.rows) != 1 {
		t.Fatalf("same evidence was not idempotent: %+v err=%v rows=%d", second, err, len(repository.rows))
	}
	if repository.rows[0].Simulation || repository.rows[0].ReasonCode != ReasonBaselineNotEligible || repository.rows[0].CandidatePolicyJSON != "" {
		t.Fatalf("blocked production record leaked a candidate: %+v", repository.rows[0])
	}
}

func TestControllerRejectsSimulationSnapshotOnProductionCycle(t *testing.T) {
	now := time.Date(2026, 9, 6, 4, 10, 0, 0, time.UTC)
	snapshot, _, err := controllerAcceptanceFixture(now)
	if err != nil {
		t.Fatal(err)
	}
	controller, _ := NewController(&fakePolicyReader{snapshot: activePolicySnapshot()}, &fakeEvaluationReader{gate: unreviewedGate()}, &memoryRecommendationRepository{}, policy.DefaultStrategyRegistry())
	if _, err := controller.Reconcile(context.Background(), snapshot); err == nil {
		t.Fatal("production controller accepted a simulation snapshot")
	}
}

func TestRepositoryValidationRejectsAppliedOrMutatedEvidence(t *testing.T) {
	now := time.Date(2026, 9, 6, 4, 15, 0, 0, time.UTC)
	policyReader := &fakePolicyReader{snapshot: activePolicySnapshot()}
	repository := &memoryRecommendationRepository{}
	controller, _ := NewController(policyReader, &fakeEvaluationReader{gate: unreviewedGate()}, repository, policy.DefaultStrategyRegistry())
	controller.clock = func() time.Time { return now }
	if _, err := controller.Acceptance(context.Background(), AcceptanceScenario); err != nil {
		t.Fatal(err)
	}
	mutated := repository.rows[1]
	mutated.Applied = true
	if err := validateRecord(mutated); err == nil {
		t.Fatal("applied recommendation was accepted")
	}
	mutated = repository.rows[1]
	mutated.EvidenceJSON = strings.Replace(mutated.EvidenceJSON, "0.7", "0.8", 1)
	if err := validateRecord(mutated); err == nil {
		t.Fatal("mutated evidence was accepted")
	}
}

func activePolicySnapshot() policy.PolicySnapshot {
	document := policy.DefaultRoutingPolicy()
	return policy.PolicySnapshot{LoadedPolicy: policy.LoadedPolicy{
		Record:   model.RoutingPolicy{Version: document.Version, Environment: policy.DefaultPolicyEnvironment, Status: policy.PolicyStatusActive, PolicyHash: strings.Repeat("a", 64)},
		Document: document, Source: "mysql",
	}, Registry: policy.DefaultStrategyRegistry().List()}
}

func unreviewedGate() EvaluationGate {
	return EvaluationGate{Source: "unified_evaluation_report", RunID: "evalrun-test", CandidateVersion: "candidate-test", ReportSHA256: strings.Repeat("b", 64), TechnicalGatesPassed: true, HumanReviewed: false, BaselineEligible: false}
}
