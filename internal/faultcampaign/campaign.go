package faultcampaign

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"GopherAI/internal/observability"
)

type scenarioDefinition struct {
	id, name, faultClass, mechanism, outcome string
	policy                                   observability.DetectionPolicy
	baseline, injectedOne, injectedTwo       float64
	recoveryOne, recoveryTwo                 float64
	indicators                               func(string, float64) ServiceIndicators
}

func BuildReport() (CampaignReport, error) {
	definitions := scenarioDefinitions()
	report := CampaignReport{
		SchemaVersion: SchemaVersion, FixtureVersion: FixtureVersion, Environment: Environment, Mode: Mode,
		Simulation: true, AffectsProduction: false, Applied: false, Scenarios: make([]ScenarioReport, 0, len(definitions)),
		Guardrails: []string{
			"只在进程内隔离验收适配器注入，不停止 Redis、RabbitMQ、模型或线上工具",
			"复用生产固定阈值与 Z-score 检测器，当前候选点不进入基线",
			"只生成 Recommend-only 反事实建议，Applied=false，线上流量与活动策略均不变化",
			"验收报告不写生产 Incident、Webhook 或控制动作指标",
		},
		Limitations: []string{
			"这是确定性隔离故障演练，不冒充生产基础设施事故或真实用户流量。",
			"MTTD 与恢复时间按 60 秒逻辑采样窗口计算，不等同于公网压测墙钟时间。",
			"由于没有执行缓解动作，Mitigation Success Rate 明确标记为 not_measured，而不是伪造为 100%。",
		},
	}
	for _, definition := range definitions {
		outcome, err := runIsolatedProbe(definition.id)
		if err != nil {
			return CampaignReport{}, err
		}
		if outcome != definition.outcome {
			return CampaignReport{}, fmt.Errorf("isolated probe %s returned unexpected outcome %s", definition.id, outcome)
		}
		scenario, err := runScenario(definition)
		if err != nil {
			return CampaignReport{}, err
		}
		report.Scenarios = append(report.Scenarios, scenario)
	}
	report.Summary = summarize(report.Scenarios)
	digest, err := reportDigest(report)
	if err != nil {
		return CampaignReport{}, err
	}
	report.ReportSHA256 = digest
	report.CampaignID = "campaign-" + digest[:20]
	return report, ValidateReport(report)
}

func ValidateReport(report CampaignReport) error {
	if report.SchemaVersion != SchemaVersion || report.FixtureVersion != FixtureVersion || report.Environment != Environment || report.Mode != Mode ||
		!report.Simulation || report.AffectsProduction || report.Applied || len(report.Scenarios) != 3 || len(report.ReportSHA256) != 64 || report.CampaignID != "campaign-"+report.ReportSHA256[:20] {
		return errors.New("fault campaign report envelope is invalid")
	}
	digest, err := reportDigest(report)
	if err != nil || digest != report.ReportSHA256 {
		return errors.New("fault campaign report hash mismatch")
	}
	for _, scenario := range report.Scenarios {
		if !scenario.Detected || !scenario.Recovered || !scenario.FixedThresholdDetected || !scenario.ZScoreDetected || scenario.AppliedCount != 0 ||
			scenario.RecommendationCount != 1 || scenario.FalsePositiveChecks < 1 || scenario.FalsePositives != 0 || len(scenario.Timeline) != 6 || len(scenario.EvidenceSHA256) != 64 {
			return fmt.Errorf("fault campaign scenario %s is invalid", scenario.ScenarioID)
		}
		for _, point := range scenario.Timeline {
			if point.Applied || point.TrafficChanged {
				return fmt.Errorf("fault campaign scenario %s changed production state", scenario.ScenarioID)
			}
		}
	}
	if report.Summary.ScenarioCount != 3 || report.Summary.DetectedCount != 3 || report.Summary.RecoveredCount != 3 || report.Summary.AppliedCount != 0 ||
		report.Summary.FalsePositives != 0 || report.Summary.MitigationSuccessState != "not_measured_observe_only" {
		return errors.New("fault campaign summary is invalid")
	}
	return nil
}

func runScenario(definition scenarioDefinition) (ScenarioReport, error) {
	baseTime := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	baseline := baselineObservations(definition, baseTime)
	injected := append(append([]observability.MetricObservation(nil), baseline...),
		observability.MetricObservation{ObservedAt: baseTime.Add(time.Minute), Value: definition.injectedOne, Population: 100},
		observability.MetricObservation{ObservedAt: baseTime.Add(2 * time.Minute), Value: definition.injectedTwo, Population: 100},
	)
	detected, err := observability.AnalyzeMetricWindow(definition.policy, injected)
	if err != nil {
		return ScenarioReport{}, err
	}
	recoveredWindow := append(append([]observability.MetricObservation(nil), injected...),
		observability.MetricObservation{ObservedAt: baseTime.Add(3 * time.Minute), Value: definition.recoveryOne, Population: 100},
		observability.MetricObservation{ObservedAt: baseTime.Add(4 * time.Minute), Value: definition.recoveryTwo, Population: 100},
	)
	recovered, err := observability.AnalyzeMetricWindow(definition.policy, recoveredWindow)
	if err != nil {
		return ScenarioReport{}, err
	}
	healthyTail := append(append([]observability.MetricObservation(nil), baseline...),
		observability.MetricObservation{ObservedAt: baseTime.Add(time.Minute), Value: definition.baseline, Population: 100},
		observability.MetricObservation{ObservedAt: baseTime.Add(2 * time.Minute), Value: definition.baseline, Population: 100},
	)
	healthy, err := observability.AnalyzeMetricWindow(definition.policy, healthyTail)
	if err != nil {
		return ScenarioReport{}, err
	}
	tracker := observability.NewIncidentTracker()
	triggered := tracker.Apply(detected.Recommendation.IncidentKey, detected.Anomalous)
	recoveryEvent := tracker.Apply(detected.Recommendation.IncidentKey, recovered.Anomalous)
	scenario := ScenarioReport{
		ScenarioID: definition.id, Name: definition.name, FaultClass: definition.faultClass, InjectionMechanism: definition.mechanism,
		InjectedOutcome: definition.outcome, Metric: definition.policy.Metric, Strategy: definition.policy.Strategy, Direction: definition.policy.Direction,
		Detected: detected.Anomalous && triggered.Type == "triggered", Recovered: !recovered.Anomalous && recoveryEvent.Type == "recovered",
		FixedThresholdDetected: detected.Fixed.Anomalous, ZScoreDetected: detected.ZScore.Anomalous,
		MTTDSeconds: 60, RecommendationDelaySeconds: 0, RecoverySeconds: 180, FalsePositiveChecks: 1,
		RecommendationCount: 1, AppliedCount: 0,
		Timeline: []TimelinePoint{
			timelinePoint("before", 0, definition.baseline, "healthy", "healthy", "none", 0, definition.indicators("before", definition.baseline)),
			timelinePoint("injected", 60, definition.injectedOne, "pending_second_point", "none", "none", 0, definition.indicators("injected", definition.injectedOne)),
			timelinePoint("detected", 120, definition.injectedTwo, detected.DecisionStatus, triggered.Type, "none", 0, definition.indicators("detected", definition.injectedTwo)),
			timelinePoint("recommendation", 120, definition.injectedTwo, detected.DecisionStatus, "duplicate_suppressed", detected.Recommendation.Action, detected.Recommendation.WeightDeltaBasis, definition.indicators("detected", definition.injectedTwo)),
			timelinePoint("probe", 180, definition.recoveryOne, "recovery_pending", "active", "observe_only_probe", 0, definition.indicators("probe", definition.recoveryOne)),
			timelinePoint("recovered", 240, definition.recoveryTwo, recovered.DecisionStatus, recoveryEvent.Type, "none", 0, definition.indicators("recovered", definition.recoveryTwo)),
		},
	}
	if healthy.Anomalous {
		scenario.FalsePositives = 1
	}
	evidence, err := scenarioDigest(scenario)
	if err != nil {
		return ScenarioReport{}, err
	}
	scenario.EvidenceSHA256 = evidence
	return scenario, nil
}

func timelinePoint(phase string, offset int, value float64, decision, incident, action string, delta int, indicators ServiceIndicators) TimelinePoint {
	return TimelinePoint{Phase: phase, OffsetSeconds: offset, MetricValue: value, DecisionStatus: decision, IncidentEvent: incident,
		RecommendationAction: action, WeightDeltaBasis: delta, Applied: false, TrafficChanged: false, Indicators: indicators}
}

func baselineObservations(definition scenarioDefinition, baseTime time.Time) []observability.MetricObservation {
	observations := make([]observability.MetricObservation, 0, 30)
	step := .002
	if definition.policy.Direction == observability.DirectionLowerIsBetter {
		step = .01
	}
	for index := 0; index < 30; index++ {
		value := definition.baseline + float64(index%5-2)*step
		observations = append(observations, observability.MetricObservation{ObservedAt: baseTime.Add(time.Duration(index-30) * time.Minute), Value: value, Population: 100})
	}
	return observations
}

func summarize(scenarios []ScenarioReport) CampaignSummary {
	summary := CampaignSummary{ScenarioCount: len(scenarios), MitigationSuccessState: "not_measured_observe_only"}
	var mttd, recovery int
	for _, scenario := range scenarios {
		if scenario.Detected {
			summary.DetectedCount++
		}
		if scenario.Recovered {
			summary.RecoveredCount++
		}
		summary.RecommendationCount += scenario.RecommendationCount
		summary.AppliedCount += scenario.AppliedCount
		summary.FalsePositiveChecks += scenario.FalsePositiveChecks
		summary.FalsePositives += scenario.FalsePositives
		mttd += scenario.MTTDSeconds
		recovery += scenario.RecoverySeconds
	}
	if summary.ScenarioCount > 0 {
		summary.DetectionRate = float64(summary.DetectedCount) / float64(summary.ScenarioCount)
		summary.RecoveryRate = float64(summary.RecoveredCount) / float64(summary.ScenarioCount)
		summary.MeanMTTDSeconds = float64(mttd) / float64(summary.ScenarioCount)
		summary.MeanRecoverySeconds = float64(recovery) / float64(summary.ScenarioCount)
	}
	if summary.FalsePositiveChecks > 0 {
		summary.FalsePositiveRate = float64(summary.FalsePositives) / float64(summary.FalsePositiveChecks)
	}
	return summary
}

func reportDigest(report CampaignReport) (string, error) {
	report.CampaignID, report.ReportSHA256 = "", ""
	encoded, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func scenarioDigest(scenario ScenarioReport) (string, error) {
	scenario.EvidenceSHA256 = ""
	encoded, err := json.Marshal(scenario)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func scenarioDefinitions() []scenarioDefinition {
	higher := func(metric, strategy string, warning, critical float64) observability.DetectionPolicy {
		return observability.DetectionPolicy{Metric: metric, Strategy: strategy, Direction: observability.DirectionHigherIsBetter, WarningThreshold: warning, CriticalThreshold: critical, MinimumPopulation: 50, WindowSize: 30, MinimumWindow: 10, ZScoreThreshold: 3, ConsecutivePoints: 2}
	}
	lower := func(metric, strategy string, warning, critical float64) observability.DetectionPolicy {
		return observability.DetectionPolicy{Metric: metric, Strategy: strategy, Direction: observability.DirectionLowerIsBetter, WarningThreshold: warning, CriticalThreshold: critical, MinimumPopulation: 50, WindowSize: 30, MinimumWindow: 10, ZScoreThreshold: 3, ConsecutivePoints: 2}
	}
	return []scenarioDefinition{
		{id: "rag_groundedness_drop", name: "RAG 有依据回答率下降", faultClass: "rag_degradation", mechanism: "空召回进入生产 Evidence Gate 并被拒答", outcome: "RAG_NO_EVIDENCE", policy: higher("rag_grounded_answer_rate", "rag_deep", .93, .90), baseline: .965, injectedOne: .72, injectedTwo: .70, recoveryOne: .95, recoveryTwo: .96, indicators: ragIndicators},
		{id: "agent_latency_spike", name: "多 Agent 延迟突增", faultClass: "agent_latency", mechanism: "阻塞 Runner 进入生产 ParallelExecutor 超时边界", outcome: "AGENT_TASK_TIMEOUT", policy: lower("agent_p95_latency_seconds", "diagnosis_collaborative", 2, 4), baseline: 1.05, injectedOne: 4.8, injectedTwo: 5.2, recoveryOne: 1.3, recoveryTwo: 1.1, indicators: agentIndicators},
		{id: "tool_timeout_burst", name: "受治理工具超时", faultClass: "tool_failure", mechanism: "隔离 Tool Adapter 连续返回稳定超时", outcome: "TOOL_TIMEOUT", policy: higher("tool_success_rate", "official_document_search", .95, .90), baseline: .99, injectedOne: .45, injectedTwo: .40, recoveryOne: .97, recoveryTwo: .99, indicators: toolIndicators},
	}
}

func ragIndicators(phase string, value float64) ServiceIndicators {
	result := ServiceIndicators{QualityRate: value, SuccessRate: .99, P95LatencyMS: 880, P99LatencyMS: 1280, InputTokens: 1450, CostMicros: 1900, Population: 100}
	if phase == "injected" || phase == "detected" {
		result.SuccessRate, result.P95LatencyMS, result.P99LatencyMS = .82, 1580, 2420
	}
	if phase == "probe" {
		result.SuccessRate, result.P95LatencyMS, result.P99LatencyMS = .95, 1080, 1600
	}
	return result
}

func agentIndicators(phase string, value float64) ServiceIndicators {
	result := ServiceIndicators{QualityRate: .95, SuccessRate: .98, P95LatencyMS: int64(value * 1000), P99LatencyMS: int64(value*1000) + 420, InputTokens: 2200, CostMicros: 3100, Population: 100}
	if phase == "injected" || phase == "detected" {
		result.SuccessRate, result.QualityRate, result.P99LatencyMS = .78, .86, int64(value*1000)+1250
	}
	if phase == "probe" {
		result.SuccessRate = .94
	}
	return result
}

func toolIndicators(phase string, value float64) ServiceIndicators {
	result := ServiceIndicators{QualityRate: .96, SuccessRate: value, P95LatencyMS: 520, P99LatencyMS: 820, InputTokens: 920, CostMicros: 1050, Population: 100}
	if phase == "injected" || phase == "detected" {
		result.QualityRate, result.P95LatencyMS, result.P99LatencyMS = .84, 4050, 5100
	}
	if phase == "probe" {
		result.P95LatencyMS, result.P99LatencyMS = 760, 1100
	}
	return result
}
