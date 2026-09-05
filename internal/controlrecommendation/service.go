package controlrecommendation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"time"

	"GopherAI/internal/observability"
	"GopherAI/internal/policy"
	"GopherAI/model"
)

type AnomalyReader interface {
	LatestProductionAnalysis(context.Context) (observability.ProductionAnomalySnapshot, error)
}

func Run(ctx context.Context, reader AnomalyReader, controller *Controller, initialDelay time.Duration, interval time.Duration, logger *log.Logger) {
	if reader == nil || controller == nil || initialDelay < 0 || interval <= 0 {
		return
	}
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		snapshot, err := reader.LatestProductionAnalysis(runCtx)
		result := ReconcileResult{}
		if err == nil {
			result, err = controller.Reconcile(runCtx, snapshot)
		}
		cancel()
		if logger != nil {
			if err != nil {
				logger.Print(`{"event":"recommend_only_controller","status":"error","reason_code":"CONTROLLER_CYCLE_FAILED"}`)
			} else {
				logger.Printf(`{"event":"recommend_only_controller","status":"success","examined":%d,"recommended":%d,"blocked":%d,"duplicate":%d}`, result.Examined, result.Recommended, result.Blocked, result.Duplicate)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (controller *Controller) Audit(ctx context.Context) (AuditSnapshot, error) {
	result := AuditSnapshot{
		SchemaVersion: SchemaVersion, Mode: ModeRecommendOnly, Latest: []RecommendationSummary{},
		Guardrails:  []string{"每周期单策略最多建议调整 10%", "健康 fallback 承接建议流量", "候选只写不可变审计", "不存在策略激活 API", "Applied 永远为 false"},
		Limitations: []string{"当前人工复核尚未完成，真实生产异常会被 baseline_not_eligible 门禁阻断。", "验收 Fixture 只证明候选计算与不可写边界，不冒充生产异常或正式基线。"},
	}
	if controller == nil || controller.repository == nil || controller.policies == nil || controller.evaluation == nil {
		return result, errors.New("recommend-only controller is unavailable")
	}
	policySnapshot, err := controller.policies.Snapshot(ctx)
	if err != nil {
		return result, err
	}
	gate, err := controller.evaluation.Load()
	if err != nil {
		return result, err
	}
	recommended, blocked, rows, err := controller.repository.Audit(ctx, 10)
	if err != nil {
		return result, err
	}
	result.Recommended, result.Blocked, result.Evaluation = recommended, blocked, gate
	result.ActivePolicy = PolicyIdentity{Version: policySnapshot.Record.Version, SHA256: policySnapshot.Record.PolicyHash, Status: policySnapshot.Record.Status}
	for _, row := range rows {
		result.Latest = append(result.Latest, publicSummary(row))
	}
	return result, nil
}

func (controller *Controller) Acceptance(ctx context.Context, scenario string) (AcceptanceResult, error) {
	result := AcceptanceResult{SchemaVersion: SchemaVersion, Simulation: true, Scenario: scenario, Guardrails: []string{"Fixture 与生产窗口分表意相同但显式标记 Simulation", "候选不写 routing_policies", "活动版本与哈希前后必须一致", "建议步长不超过 10%"}}
	if controller == nil || scenario != AcceptanceScenario {
		return result, errors.New("controller acceptance scenario is invalid")
	}
	before, err := controller.policies.Snapshot(ctx)
	if err != nil {
		return result, err
	}
	actualGate, err := controller.evaluation.Load()
	if err != nil {
		return result, err
	}
	now := controller.clock().UTC()
	snapshot, series, err := controllerAcceptanceFixture(now)
	if err != nil {
		return result, err
	}
	blocked, err := controller.buildDecision(snapshot, series, before, actualGate, true, "deterministic_acceptance_actual_gate", now)
	if err != nil {
		return result, err
	}
	if _, err := controller.repository.Create(ctx, blocked); err != nil {
		return result, err
	}
	fixtureGate := EvaluationGate{
		Source: "deterministic_acceptance_fixture", RunID: "evalrun-controller-fixture-v1", CandidateVersion: "reviewed-baseline-fixture-v1",
		ReportSHA256: hashString("reviewed-baseline-fixture-v1"), TechnicalGatesPassed: true, HumanReviewed: true, BaselineEligible: true,
	}
	recommended, err := controller.buildDecision(snapshot, series, before, fixtureGate, true, "deterministic_acceptance_eligible_gate", now.Add(time.Nanosecond))
	if err != nil {
		return result, err
	}
	if _, err := controller.repository.Create(ctx, recommended); err != nil {
		return result, err
	}
	after, err := controller.policies.Snapshot(ctx)
	if err != nil {
		return result, err
	}
	result.BaselineGuard, result.EligibleFixture = publicSummary(blocked), publicSummary(recommended)
	result.ActivePolicyBefore = PolicyIdentity{Version: before.Record.Version, SHA256: before.Record.PolicyHash, Status: before.Record.Status}
	result.ActivePolicyAfter = PolicyIdentity{Version: after.Record.Version, SHA256: after.Record.PolicyHash, Status: after.Record.Status}
	result.ActivePolicyUnchanged = result.ActivePolicyBefore == result.ActivePolicyAfter
	if !result.ActivePolicyUnchanged || recommended.Status != StatusRecommended || recommended.Applied || recommended.WeightDeltaBasis < -maximumWeightStepBasis {
		return result, errors.New("controller acceptance guardrails failed")
	}
	return result, nil
}

func controllerAcceptanceFixture(now time.Time) (observability.ProductionAnomalySnapshot, observability.ProductionMetricAnalysis, error) {
	detectionPolicy, observations, err := observability.AcceptanceAnomalyScenario("quality_drop", now)
	if err != nil {
		return observability.ProductionAnomalySnapshot{}, observability.ProductionMetricAnalysis{}, err
	}
	detectionPolicy.Strategy = policy.RAGFastStrategyName
	analysis, err := observability.AnalyzeMetricWindow(detectionPolicy, observations)
	if err != nil || analysis.DecisionStatus != "anomalous" {
		return observability.ProductionAnomalySnapshot{}, observability.ProductionMetricAnalysis{}, errors.New("controller acceptance anomaly fixture is invalid")
	}
	batchID := stableRecommendationID("acceptance", now.Format(time.RFC3339Nano))
	rulesSHA := hashString("controller-acceptance-rules-v1")
	latest := observations[len(observations)-1]
	point := observability.MetricWindowPoint{Metric: detectionPolicy.Metric, Strategy: detectionPolicy.Strategy, DataStatus: observability.MetricWindowObserved, Value: latest.Value, Population: latest.Population, WindowSeconds: 900, ObservedAt: latest.ObservedAt.UTC()}
	series := observability.ProductionMetricAnalysis{Metric: detectionPolicy.Metric, Strategy: detectionPolicy.Strategy, WindowSeconds: 900, DataStatus: observability.MetricWindowObserved, HistoryPointCount: len(observations), Latest: point, Analysis: &analysis}
	snapshot := observability.ProductionAnomalySnapshot{SchemaVersion: observability.ProductionAnomalySchemaVersion, Status: "anomalous", Source: "deterministic_acceptance_fixture", Simulation: true, BatchID: batchID, CollectedAt: now, RulesVersion: "controller-acceptance-rules-v1", RulesSHA256: rulesSHA, Series: []observability.ProductionMetricAnalysis{series}}
	return snapshot, series, nil
}

func publicSummary(row model.ControlRecommendation) RecommendationSummary {
	return RecommendationSummary{
		RecommendationID: row.RecommendationID, Status: row.Status, Simulation: row.Simulation, Source: row.Source,
		Metric: row.Metric, Strategy: row.Strategy, Intent: row.Intent, ReasonCode: row.ReasonCode,
		ParentPolicyVersion: row.ParentPolicyVersion, ParentPolicySHA256: row.ParentPolicySHA256,
		CandidatePolicyVersion: row.CandidatePolicyVersion, CandidatePolicySHA256: row.CandidatePolicySHA256,
		BeforeWeightBasis: row.BeforeWeightBasis, ProposedWeightBasis: row.ProposedWeightBasis,
		FallbackStrategy: row.FallbackStrategy, FallbackBeforeBasis: row.FallbackBeforeBasis, FallbackProposedBasis: row.FallbackProposedBasis,
		WeightDeltaBasis: row.WeightDeltaBasis, EvidenceSHA256: row.EvidenceSHA256, EvaluationRunID: row.EvaluationRunID,
		EvaluationReportSHA256: row.EvaluationReportSHA256, BaselineEligible: row.BaselineEligible, Applied: row.Applied, CreatedAt: row.CreatedAt.UTC(),
	}
}

func hashString(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
