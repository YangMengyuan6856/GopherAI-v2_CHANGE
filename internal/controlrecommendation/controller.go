package controlrecommendation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"GopherAI/internal/observability"
	"GopherAI/internal/policy"
	"GopherAI/model"
)

const maximumWeightStepBasis = 1000
const minimumExplorationBasis = 500

type PolicyReader interface {
	Snapshot(context.Context) (policy.PolicySnapshot, error)
}

type EvaluationReader interface {
	Load() (EvaluationGate, error)
}

type RecommendationRepository interface {
	Create(context.Context, model.ControlRecommendation) (bool, error)
	Audit(context.Context, int) (int64, int64, []model.ControlRecommendation, error)
}

type Observer interface {
	RecordControlAction(action string, result string)
	ObserveControlLoop(result string, duration time.Duration)
}

type Controller struct {
	policies   PolicyReader
	evaluation EvaluationReader
	repository RecommendationRepository
	registry   *policy.StrategyRegistry
	observer   Observer
	clock      func() time.Time
}

type ReconcileResult struct {
	Examined    int `json:"examined"`
	Recommended int `json:"recommended"`
	Blocked     int `json:"blocked"`
	Duplicate   int `json:"duplicate"`
}

type decisionEvidence struct {
	SchemaVersion string                          `json:"schema_version"`
	Source        string                          `json:"source"`
	Simulation    bool                            `json:"simulation"`
	BatchID       string                          `json:"batch_id"`
	RulesVersion  string                          `json:"rules_version"`
	RulesSHA256   string                          `json:"rules_sha256"`
	Metric        string                          `json:"metric"`
	Strategy      string                          `json:"strategy"`
	Observation   observability.MetricWindowPoint `json:"observation"`
	Analysis      observability.AnomalyAnalysis   `json:"analysis"`
	Evaluation    EvaluationGate                  `json:"evaluation"`
	ParentPolicy  PolicyIdentity                  `json:"parent_policy"`
}

func NewController(policies PolicyReader, evaluation EvaluationReader, repository RecommendationRepository, registry *policy.StrategyRegistry, observers ...Observer) (*Controller, error) {
	if policies == nil || evaluation == nil || repository == nil || registry == nil {
		return nil, errors.New("recommend-only controller dependencies are required")
	}
	result := &Controller{policies: policies, evaluation: evaluation, repository: repository, registry: registry, clock: time.Now}
	if len(observers) > 0 {
		result.observer = observers[0]
	}
	return result, nil
}

func (controller *Controller) Reconcile(ctx context.Context, snapshot observability.ProductionAnomalySnapshot) (ReconcileResult, error) {
	result := ReconcileResult{}
	started := time.Now()
	if controller == nil || controller.clock == nil || snapshot.Simulation || len(snapshot.BatchID) != 64 || snapshot.RulesVersion == "" || len(snapshot.RulesSHA256) != 64 {
		return result, errors.New("production controller snapshot provenance is invalid")
	}
	policySnapshot, err := controller.policies.Snapshot(ctx)
	if err != nil {
		return result, errors.New("load active policy for controller failed")
	}
	gate, err := controller.evaluation.Load()
	if err != nil {
		return result, errors.New("load evaluation gate for controller failed")
	}
	var joined error
	for _, series := range snapshot.Series {
		if series.Analysis == nil || series.DataStatus != observability.MetricWindowObserved || series.Analysis.DecisionStatus != "anomalous" {
			continue
		}
		result.Examined++
		record, decisionErr := controller.buildDecision(snapshot, series, policySnapshot, gate, false, "production_metric_window", controller.clock().UTC())
		if decisionErr != nil {
			joined = errors.Join(joined, decisionErr)
			continue
		}
		created, createErr := controller.repository.Create(ctx, record)
		if createErr != nil {
			joined = errors.Join(joined, createErr)
			continue
		}
		if !created {
			result.Duplicate++
			controller.recordDecision("duplicate")
			continue
		}
		if record.Status == StatusRecommended {
			result.Recommended++
			controller.recordDecision(StatusRecommended)
		} else {
			result.Blocked++
			controller.recordDecision(StatusBlocked)
		}
	}
	if controller.observer != nil {
		outcome := "no_anomaly"
		if joined != nil {
			outcome = "error"
		} else if result.Examined > 0 {
			outcome = "success"
		}
		controller.observer.ObserveControlLoop(outcome, time.Since(started))
	}
	return result, joined
}

func (controller *Controller) recordDecision(result string) {
	if controller != nil && controller.observer != nil {
		controller.observer.RecordControlAction("reduce_weight", result)
	}
}

func (controller *Controller) buildDecision(snapshot observability.ProductionAnomalySnapshot, series observability.ProductionMetricAnalysis, policySnapshot policy.PolicySnapshot, gate EvaluationGate, simulation bool, source string, now time.Time) (model.ControlRecommendation, error) {
	if series.Analysis == nil || series.Analysis.DecisionStatus != "anomalous" || series.Analysis.Recommendation.Applied ||
		series.Analysis.Recommendation.Mode != ModeRecommendOnly || series.Analysis.Recommendation.Action != "reduce_candidate_weight" {
		return model.ControlRecommendation{}, errors.New("detector recommendation is not eligible for controller evaluation")
	}
	parent := PolicyIdentity{Version: policySnapshot.Record.Version, SHA256: policySnapshot.Record.PolicyHash, Status: policySnapshot.Record.Status}
	if parent.Version == "" || len(parent.SHA256) != 64 || parent.Status != policy.PolicyStatusActive {
		return model.ControlRecommendation{}, errors.New("active policy identity is invalid")
	}
	if err := validateEvaluationGate(gate); err != nil {
		return model.ControlRecommendation{}, err
	}
	evidence := decisionEvidence{
		SchemaVersion: SchemaVersion, Source: source, Simulation: simulation, BatchID: snapshot.BatchID,
		RulesVersion: snapshot.RulesVersion, RulesSHA256: snapshot.RulesSHA256,
		Metric: series.Metric, Strategy: series.Strategy, Observation: series.Latest,
		Analysis: *series.Analysis, Evaluation: gate, ParentPolicy: parent,
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return model.ControlRecommendation{}, err
	}
	evidenceDigest := sha256.Sum256(evidenceJSON)
	evidenceSHA := hex.EncodeToString(evidenceDigest[:])
	// One detector incident may span many minute batches. Keep one immutable
	// decision for the same incident, policy, evaluation and rules identity so
	// a persistent anomaly cannot flood the audit table every minute.
	recommendationID := stableRecommendationID(series.Analysis.Recommendation.IncidentKey, series.Metric, series.Strategy, parent.Version, parent.SHA256, gate.ReportSHA256, snapshot.RulesSHA256, source)
	record := model.ControlRecommendation{
		RecommendationID: recommendationID, Mode: ModeRecommendOnly, Status: StatusBlocked,
		Simulation: simulation, Source: source, IncidentKey: series.Analysis.Recommendation.IncidentKey,
		BatchID: snapshot.BatchID, Metric: series.Metric, Strategy: series.Strategy,
		ParentPolicyVersion: parent.Version, ParentPolicySHA256: parent.SHA256,
		ReasonCode: ReasonBaselineNotEligible, EvidenceJSON: string(evidenceJSON), EvidenceSHA256: evidenceSHA,
		EvaluationRunID: gate.RunID, EvaluationReportSHA256: gate.ReportSHA256, BaselineEligible: gate.BaselineEligible,
		DetectorVersion: series.Analysis.DetectorVersion, RulesVersion: snapshot.RulesVersion, RulesSHA256: snapshot.RulesSHA256,
		Applied: false, CreatedAt: now.UTC(),
	}
	if !gate.TechnicalGatesPassed || !gate.HumanReviewed || !gate.BaselineEligible {
		return record, nil
	}
	intent, rule, found := findActiveRule(policySnapshot.Document, series.Strategy)
	if !found {
		record.ReasonCode = ReasonStrategyNotInPolicy
		return record, nil
	}
	record.Intent = intent
	candidate, before, after, fallbackBefore, fallbackAfter, fallback, reason, err := buildCandidatePolicy(policySnapshot.Document, controller.registry, intent, rule, series.Strategy, recommendationID)
	if err != nil {
		return model.ControlRecommendation{}, err
	}
	record.BeforeWeightBasis, record.ProposedWeightBasis = before, after
	record.FallbackBeforeBasis, record.FallbackProposedBasis, record.FallbackStrategy = fallbackBefore, fallbackAfter, fallback
	record.WeightDeltaBasis = after - before
	if reason != ReasonCandidateCreated {
		record.ReasonCode = reason
		return record, nil
	}
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		return model.ControlRecommendation{}, err
	}
	candidateDigest := sha256.Sum256(candidateJSON)
	record.Status, record.ReasonCode = StatusRecommended, ReasonCandidateCreated
	record.CandidatePolicyVersion, record.CandidatePolicySHA256 = candidate.Version, hex.EncodeToString(candidateDigest[:])
	record.CandidatePolicyJSON = string(candidateJSON)
	return record, nil
}

func validateEvaluationGate(gate EvaluationGate) error {
	if strings.TrimSpace(gate.Source) == "" || strings.TrimSpace(gate.RunID) == "" || strings.TrimSpace(gate.CandidateVersion) == "" || len(gate.ReportSHA256) != 64 {
		return errors.New("controller evaluation gate identity is invalid")
	}
	if gate.BaselineEligible && (!gate.TechnicalGatesPassed || !gate.HumanReviewed) {
		return errors.New("controller evaluation gate is inconsistent")
	}
	return nil
}

func findActiveRule(document policy.RoutingPolicyDocument, strategy string) (string, policy.RoutingRule, bool) {
	intents := make([]string, 0, len(document.Rules))
	for intent := range document.Rules {
		intents = append(intents, intent)
	}
	sort.Strings(intents)
	for _, intent := range intents {
		rule := document.Rules[intent]
		for _, weighted := range rule.Strategies {
			if weighted.Name == strategy {
				return intent, rule, true
			}
		}
	}
	return "", policy.RoutingRule{}, false
}

func buildCandidatePolicy(document policy.RoutingPolicyDocument, registry *policy.StrategyRegistry, intent string, rule policy.RoutingRule, strategy string, recommendationID string) (policy.RoutingPolicyDocument, int, int, int, int, string, string, error) {
	candidate := policy.RoutingPolicyDocument{SchemaVersion: document.SchemaVersion, Version: "recommend-" + recommendationID[:16], SeedSalt: document.SeedSalt, Rules: make(map[string]policy.RoutingRule, len(document.Rules))}
	for ruleIntent, existing := range document.Rules {
		candidate.Rules[ruleIntent] = policy.RoutingRule{Strategies: append([]policy.WeightedStrategy(nil), existing.Strategies...), Fallback: existing.Fallback}
	}
	fallback := strings.TrimSpace(rule.Fallback)
	if fallback == "" || fallback == strategy {
		return candidate, 0, 0, 0, 0, fallback, ReasonNoFallback, nil
	}
	before, fallbackBefore, strategyIndex, fallbackIndex := 0, 0, -1, -1
	for index, weighted := range rule.Strategies {
		if weighted.Name == strategy {
			before, strategyIndex = weighted.WeightBasis, index
		}
		if weighted.Name == fallback {
			fallbackBefore, fallbackIndex = weighted.WeightBasis, index
		}
	}
	if strategyIndex < 0 {
		return candidate, 0, 0, fallbackBefore, fallbackBefore, fallback, ReasonStrategyNotInPolicy, nil
	}
	step := maximumWeightStepBasis
	if before-step < minimumExplorationBasis {
		step = before - minimumExplorationBasis
	}
	if step <= 0 {
		return candidate, before, before, fallbackBefore, fallbackBefore, fallback, ReasonExplorationFloor, nil
	}
	updated := candidate.Rules[intent]
	updated.Strategies[strategyIndex].WeightBasis = before - step
	if fallbackIndex >= 0 {
		updated.Strategies[fallbackIndex].WeightBasis += step
	} else {
		updated.Strategies = append(updated.Strategies, policy.WeightedStrategy{Name: fallback, WeightBasis: step})
	}
	candidate.Rules[intent] = updated
	if err := candidate.Validate(registry); err != nil {
		return policy.RoutingPolicyDocument{}, 0, 0, 0, 0, "", "", fmt.Errorf("candidate policy validation failed: %w", err)
	}
	return candidate, before, before - step, fallbackBefore, fallbackBefore + step, fallback, ReasonCandidateCreated, nil
}

func stableRecommendationID(parts ...string) string {
	digest := sha256.Sum256([]byte(SchemaVersion + "|" + strings.Join(parts, "|")))
	return hex.EncodeToString(digest[:])
}
