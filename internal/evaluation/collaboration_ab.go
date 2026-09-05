package evaluation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sort"
	"strings"
	"time"

	"GopherAI/internal/diagnostic"
	"GopherAI/internal/orchestration"
)

const (
	CollaborationEvaluatorVersion = "collaboration-paired-evaluator-v1"
	CollaborationDatasetVersion   = "devsupport-collaboration-ab-v1"
	CollaborationCaseCount        = 20
	CollaborationTargetCaseCount  = 10
	CollaborationGuardCaseCount   = 10

	CollaborationSliceTarget = "complex_target"
	CollaborationSliceGuard  = "simple_guard"
)

type CollaborationExpected struct {
	EvidenceIDs  []string `json:"evidence_ids"`
	RootCauseIDs []string `json:"root_cause_ids"`
}

type CollaborationCase struct {
	ID             string                `json:"id"`
	Slice          string                `json:"slice"`
	Question       string                `json:"question"`
	Expected       CollaborationExpected `json:"expected"`
	Tags           []string              `json:"tags"`
	ReviewedBy     string                `json:"reviewed_by"`
	DatasetVersion string                `json:"dataset_version"`
}

type StandardDiagnosticAnalyzer interface {
	AnalyzeContext(context.Context, string) (diagnostic.ExtractedInput, diagnostic.Result, error)
}

type CollaborationCandidateRunner interface {
	Run(context.Context, orchestration.ExecutionInput) (orchestration.CollaborationRun, error)
}

type CollaborationCaseResult struct {
	ID                       string   `json:"id"`
	Slice                    string   `json:"slice"`
	BaselineQuality          float64  `json:"baseline_quality"`
	CandidateQuality         float64  `json:"candidate_quality"`
	QualityDelta             float64  `json:"quality_delta"`
	BaselineRootCauseRecall  float64  `json:"baseline_root_cause_recall"`
	CandidateRootCauseRecall float64  `json:"candidate_root_cause_recall"`
	CandidateEvidenceRecall  float64  `json:"candidate_evidence_recall"`
	BaselineLatencyMS        int64    `json:"baseline_latency_ms"`
	CandidateLatencyMS       int64    `json:"candidate_latency_ms"`
	CandidateTriggered       bool     `json:"candidate_triggered"`
	CandidateStatus          string   `json:"candidate_status"`
	AgentCount               int      `json:"agent_count"`
	InputTokens              int      `json:"input_tokens"`
	OutputTokens             int      `json:"output_tokens"`
	CostMicros               int64    `json:"cost_micros"`
	SafetyPassed             bool     `json:"safety_passed"`
	ReasonCodes              []string `json:"reason_codes,omitempty"`
	Error                    string   `json:"error,omitempty"`
}

type CollaborationABMetrics struct {
	CaseCount                  int     `json:"case_count"`
	TargetCaseCount            int     `json:"target_case_count"`
	SimpleGuardCaseCount       int     `json:"simple_guard_case_count"`
	BaselineMeanQuality        float64 `json:"baseline_mean_quality"`
	CandidateMeanQuality       float64 `json:"candidate_mean_quality"`
	MeanQualityDelta           float64 `json:"mean_quality_delta"`
	QualityDeltaCI95Lower      float64 `json:"quality_delta_ci95_lower"`
	QualityDeltaCI95Upper      float64 `json:"quality_delta_ci95_upper"`
	BaselineP95LatencyMS       float64 `json:"baseline_p95_latency_ms"`
	CandidateP95LatencyMS      float64 `json:"candidate_p95_latency_ms"`
	CandidateMeanInputTokens   float64 `json:"candidate_mean_input_tokens"`
	CandidateMeanOutputTokens  float64 `json:"candidate_mean_output_tokens"`
	CandidateMeanCostMicros    float64 `json:"candidate_mean_cost_micros"`
	TargetTriggerRate          float64 `json:"target_trigger_rate"`
	SimpleFalseTriggerRate     float64 `json:"simple_false_trigger_rate"`
	CandidateCompleteRate      float64 `json:"candidate_complete_rate"`
	SafeOutcomeRate            float64 `json:"safe_outcome_rate"`
	MaximumObservedAgents      int     `json:"maximum_observed_agents"`
	BudgetViolationCount       int     `json:"budget_violation_count"`
	SafetyViolationCount       int     `json:"safety_violation_count"`
	CandidateExecutionFailures int     `json:"candidate_execution_failures"`
}

type CollaborationRuntime struct {
	DatasetSHA256        string `json:"dataset_sha256,omitempty"`
	FixtureSHA256        string `json:"fixture_sha256,omitempty"`
	PlannerVersion       string `json:"planner_version"`
	ExecutorVersion      string `json:"executor_version"`
	SynthesizerVersion   string `json:"synthesizer_version"`
	EmbeddingModel       string `json:"embedding_model,omitempty"`
	ChatModel            string `json:"chat_model,omitempty"`
	Environment          string `json:"environment,omitempty"`
	ExternalModelMutable bool   `json:"external_model_mutable"`
}

type CollaborationABReport struct {
	SchemaVersion            string                    `json:"schema_version"`
	EvaluatorVersion         string                    `json:"evaluator_version"`
	DatasetVersion           string                    `json:"dataset_version"`
	CandidateVersion         string                    `json:"candidate_version"`
	GeneratedAt              time.Time                 `json:"generated_at"`
	HumanReviewed            bool                      `json:"human_reviewed"`
	BaselineEligible         bool                      `json:"baseline_eligible"`
	TechnicalGatesPassed     bool                      `json:"technical_gates_passed"`
	NetBenefitPassed         bool                      `json:"net_benefit_passed"`
	PromotionEligible        bool                      `json:"promotion_eligible"`
	DefaultTrafficEnabled    bool                      `json:"default_traffic_enabled"`
	RecommendedDefaultWeight int                       `json:"recommended_default_weight"`
	GateFailures             []string                  `json:"gate_failures,omitempty"`
	MetricNotes              map[string]string         `json:"metric_notes"`
	Metrics                  CollaborationABMetrics    `json:"metrics"`
	Runtime                  CollaborationRuntime      `json:"runtime"`
	Cases                    []CollaborationCaseResult `json:"cases"`
}

func LoadCollaborationCases(reader io.Reader) ([]CollaborationCase, error) {
	if reader == nil {
		return nil, errors.New("collaboration dataset reader is required")
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	cases := make([]CollaborationCase, 0, CollaborationCaseCount)
	seen := make(map[string]struct{}, CollaborationCaseCount)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		item := CollaborationCase{}
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&item); err != nil {
			return nil, fmt.Errorf("decode collaboration dataset line %d: %w", line, err)
		}
		if err := validateCollaborationCase(item); err != nil {
			return nil, fmt.Errorf("collaboration dataset line %d: %w", line, err)
		}
		if _, exists := seen[item.ID]; exists {
			return nil, fmt.Errorf("collaboration dataset line %d: duplicate id %s", line, item.ID)
		}
		seen[item.ID] = struct{}{}
		cases = append(cases, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(cases) != CollaborationCaseCount {
		return nil, fmt.Errorf("collaboration dataset must contain %d cases, got %d", CollaborationCaseCount, len(cases))
	}
	targets, guards := 0, 0
	for _, item := range cases {
		if item.Slice == CollaborationSliceTarget {
			targets++
		} else {
			guards++
		}
	}
	if targets != CollaborationTargetCaseCount || guards != CollaborationGuardCaseCount {
		return nil, fmt.Errorf("collaboration dataset requires %d target and %d guard cases, got %d and %d", CollaborationTargetCaseCount, CollaborationGuardCaseCount, targets, guards)
	}
	return cases, nil
}

func EvaluateCollaborationAB(ctx context.Context, cases []CollaborationCase, tenantID string, userID string, candidateVersion string, generatedAt time.Time, standard StandardDiagnosticAnalyzer, candidate CollaborationCandidateRunner) (CollaborationABReport, error) {
	if standard == nil || candidate == nil {
		return CollaborationABReport{}, errors.New("standard and collaborative candidates are required")
	}
	if len(cases) != CollaborationCaseCount {
		return CollaborationABReport{}, fmt.Errorf("collaboration evaluation requires %d cases", CollaborationCaseCount)
	}
	report := CollaborationABReport{
		SchemaVersion: "collaboration-paired-ab-v1", EvaluatorVersion: CollaborationEvaluatorVersion,
		DatasetVersion: CollaborationDatasetVersion, CandidateVersion: strings.TrimSpace(candidateVersion), GeneratedAt: generatedAt.UTC(),
		HumanReviewed: true, DefaultTrafficEnabled: false, RecommendedDefaultWeight: 0,
		MetricNotes: map[string]string{
			"quality":                   "复杂目标切片按同一 rubric 成对评分：根因召回 40%、项目证据召回 40%、引用/只读安全 20%。",
			"confidence_interval":       "质量差使用目标切片的 2000 次固定随机种子 paired bootstrap 百分位 95% CI。",
			"simple_false_trigger_rate": "简单门禁样本只要被规划为 collaborative 或实际创建子 Agent 即计为误触发。",
			"promotion":                 "技术门通过仍不足以切流；必须完成人工标签复核，且外部模型报告需在密封留出集复跑。",
		},
		Cases: make([]CollaborationCaseResult, 0, len(cases)),
	}
	for _, item := range cases {
		if item.ReviewedBy != "human" {
			report.HumanReviewed = false
		}
		caseResult := evaluateCollaborationCase(ctx, item, strings.TrimSpace(tenantID), strings.TrimSpace(userID), standard, candidate)
		report.Cases = append(report.Cases, caseResult)
	}
	report.Metrics = aggregateCollaborationMetrics(report.Cases)
	report.GateFailures = collaborationGateFailures(report.Metrics)
	report.TechnicalGatesPassed = len(report.GateFailures) == 0
	report.NetBenefitPassed = report.Metrics.MeanQualityDelta >= .03 && report.Metrics.QualityDeltaCI95Lower >= .03 && report.Metrics.SafetyViolationCount == 0
	report.BaselineEligible = report.TechnicalGatesPassed && report.HumanReviewed
	report.PromotionEligible = report.BaselineEligible && report.NetBenefitPassed
	return report, nil
}

func evaluateCollaborationCase(ctx context.Context, item CollaborationCase, tenantID string, userID string, standard StandardDiagnosticAnalyzer, candidate CollaborationCandidateRunner) CollaborationCaseResult {
	result := CollaborationCaseResult{ID: item.ID, Slice: item.Slice, SafetyPassed: true, ReasonCodes: []string{}}
	baselineStarted := time.Now()
	_, baseline, baselineErr := standard.AnalyzeContext(ctx, item.Question)
	result.BaselineLatencyMS = time.Since(baselineStarted).Milliseconds()
	if baselineErr == nil && item.Slice == CollaborationSliceTarget {
		result.BaselineRootCauseRecall = rootCauseRecall(diagnosticHypothesisIDs(baseline), item.Expected.RootCauseIDs)
		result.BaselineQuality = .4*result.BaselineRootCauseRecall + .2*diagnosticSafetyScore(baseline)
	}
	candidateStarted := time.Now()
	run, candidateErr := candidate.Run(ctx, orchestration.ExecutionInput{TenantID: tenantID, UserID: userID, Message: item.Question})
	result.CandidateLatencyMS = time.Since(candidateStarted).Milliseconds()
	result.CandidateTriggered = run.Executed || run.Plan.Decision == orchestration.DecisionCollaborative
	result.CandidateStatus = run.Status
	if run.Execution != nil {
		result.AgentCount = len(run.Execution.TaskResults)
		result.InputTokens = run.Execution.Usage.InputTokens
		result.OutputTokens = run.Execution.Usage.OutputTokens
		result.CostMicros = run.Execution.Usage.CostMicros
	}
	result.SafetyPassed, result.ReasonCodes = collaborationSafety(run)
	if baselineErr != nil {
		result.Error = "baseline: " + baselineErr.Error()
	}
	if candidateErr != nil {
		if result.Error != "" {
			result.Error += "; "
		}
		result.Error += "candidate: " + candidateErr.Error()
	}
	if item.Slice == CollaborationSliceTarget && candidateErr == nil {
		result.CandidateRootCauseRecall = rootCauseRecall(collaborationClaimIDs(run), item.Expected.RootCauseIDs)
		result.CandidateEvidenceRecall = evidenceRecall(collaborationEvidenceIDs(run), item.Expected.EvidenceIDs)
		grounded := collaborationGroundingScore(run)
		result.CandidateQuality = .4*result.CandidateRootCauseRecall + .4*result.CandidateEvidenceRecall + .2*grounded
		result.QualityDelta = result.CandidateQuality - result.BaselineQuality
	}
	return result
}

func aggregateCollaborationMetrics(cases []CollaborationCaseResult) CollaborationABMetrics {
	metrics := CollaborationABMetrics{CaseCount: len(cases)}
	baselineLatencies, candidateLatencies, deltas := []float64{}, []float64{}, []float64{}
	var baselineQuality, candidateQuality float64
	var inputTokens, outputTokens int
	var costMicros int64
	targetTriggers, guardTriggers, complete, safeOutcomes := 0, 0, 0, 0
	for _, item := range cases {
		if item.AgentCount > metrics.MaximumObservedAgents {
			metrics.MaximumObservedAgents = item.AgentCount
		}
		if !item.SafetyPassed {
			metrics.SafetyViolationCount++
		}
		if containsReason(item.ReasonCodes, "budget_exceeded") {
			metrics.BudgetViolationCount++
		}
		if item.Error != "" {
			metrics.CandidateExecutionFailures++
		}
		if item.Slice == CollaborationSliceGuard {
			metrics.SimpleGuardCaseCount++
			if item.CandidateTriggered {
				guardTriggers++
			}
			continue
		}
		metrics.TargetCaseCount++
		if item.CandidateTriggered {
			targetTriggers++
		}
		if item.CandidateStatus == orchestration.SynthesisComplete {
			complete++
		}
		if item.CandidateStatus == orchestration.SynthesisComplete || item.CandidateStatus == orchestration.SynthesisPartial || item.CandidateStatus == orchestration.SynthesisConflict || item.CandidateStatus == orchestration.SynthesisInsufficient {
			safeOutcomes++
		}
		baselineQuality += item.BaselineQuality
		candidateQuality += item.CandidateQuality
		deltas = append(deltas, item.QualityDelta)
		baselineLatencies = append(baselineLatencies, float64(item.BaselineLatencyMS))
		candidateLatencies = append(candidateLatencies, float64(item.CandidateLatencyMS))
		inputTokens += item.InputTokens
		outputTokens += item.OutputTokens
		costMicros += item.CostMicros
	}
	if metrics.TargetCaseCount > 0 {
		count := float64(metrics.TargetCaseCount)
		metrics.BaselineMeanQuality = baselineQuality / count
		metrics.CandidateMeanQuality = candidateQuality / count
		metrics.MeanQualityDelta = metrics.CandidateMeanQuality - metrics.BaselineMeanQuality
		metrics.TargetTriggerRate = float64(targetTriggers) / count
		metrics.CandidateCompleteRate = float64(complete) / count
		metrics.SafeOutcomeRate = float64(safeOutcomes) / count
		metrics.CandidateMeanInputTokens = float64(inputTokens) / count
		metrics.CandidateMeanOutputTokens = float64(outputTokens) / count
		metrics.CandidateMeanCostMicros = float64(costMicros) / count
	}
	if metrics.SimpleGuardCaseCount > 0 {
		metrics.SimpleFalseTriggerRate = float64(guardTriggers) / float64(metrics.SimpleGuardCaseCount)
	}
	metrics.BaselineP95LatencyMS = collaborationPercentile(baselineLatencies, .95)
	metrics.CandidateP95LatencyMS = collaborationPercentile(candidateLatencies, .95)
	metrics.QualityDeltaCI95Lower, metrics.QualityDeltaCI95Upper = pairedBootstrapCI(deltas, 2000)
	return metrics
}

func collaborationGateFailures(metrics CollaborationABMetrics) []string {
	failures := []string{}
	if metrics.TargetCaseCount != CollaborationTargetCaseCount || metrics.SimpleGuardCaseCount != CollaborationGuardCaseCount {
		failures = append(failures, "dataset_slice_count_invalid")
	}
	if metrics.TargetTriggerRate != 1 {
		failures = append(failures, "target_trigger_rate_below_1.00")
	}
	if metrics.SimpleFalseTriggerRate != 0 {
		failures = append(failures, "simple_false_trigger_rate_nonzero")
	}
	if metrics.MeanQualityDelta < .03 {
		failures = append(failures, "mean_quality_delta_below_0.03")
	}
	if metrics.QualityDeltaCI95Lower < .03 {
		failures = append(failures, "quality_delta_ci95_lower_below_0.03")
	}
	if metrics.MaximumObservedAgents > 2 {
		failures = append(failures, "maximum_observed_agents_above_2")
	}
	if metrics.BudgetViolationCount != 0 {
		failures = append(failures, "budget_violation_nonzero")
	}
	if metrics.SafetyViolationCount != 0 {
		failures = append(failures, "safety_violation_nonzero")
	}
	if metrics.CandidateExecutionFailures != 0 {
		failures = append(failures, "candidate_execution_failure_nonzero")
	}
	return failures
}

func collaborationSafety(run orchestration.CollaborationRun) (bool, []string) {
	reasons := []string{}
	if run.Mode != "shadow_only" || run.AffectsLiveTraffic || run.Plan.AffectsLiveTraffic {
		reasons = append(reasons, "live_traffic_boundary_failed")
	}
	if len(run.Plan.Tasks) > 2 || run.Plan.Budget.MaxAgents > 2 {
		reasons = append(reasons, "agent_limit_exceeded")
	}
	for _, task := range run.Plan.Tasks {
		if task.MaySpawnAgents {
			reasons = append(reasons, "recursive_agent_enabled")
		}
	}
	if run.Execution != nil {
		usage := run.Execution.Usage
		budget := run.Plan.Budget
		if usage.Agents > budget.MaxAgents || usage.ToolCalls > budget.MaxToolCalls || usage.Iterations > budget.MaxIterations || usage.InputTokens > budget.MaxInputTokens || usage.OutputTokens > budget.MaxOutputTokens || usage.CostMicros > budget.MaxCostMicros || run.Execution.Status == orchestration.ExecutionBudget {
			reasons = append(reasons, "budget_exceeded")
		}
	}
	if run.Synthesis != nil && !citationsAreClosed(run.Synthesis) {
		reasons = append(reasons, "citation_integrity_failed")
	}
	return len(reasons) == 0, reasons
}

func citationsAreClosed(synthesis *orchestration.SynthesisResult) bool {
	evidence := make(map[string]struct{}, len(synthesis.Evidence))
	for _, item := range synthesis.Evidence {
		evidence[item.ID] = struct{}{}
	}
	citations := make(map[string]struct{}, len(synthesis.Citations))
	for _, citation := range synthesis.Citations {
		if _, exists := evidence[citation.EvidenceID]; !exists {
			return false
		}
		citations[citation.CitationID] = struct{}{}
	}
	for _, claim := range synthesis.Claims {
		if len(claim.CitationIDs) == 0 {
			return false
		}
		for _, citationID := range claim.CitationIDs {
			if _, exists := citations[citationID]; !exists {
				return false
			}
		}
	}
	return true
}

func collaborationGroundingScore(run orchestration.CollaborationRun) float64 {
	if run.Synthesis == nil || len(run.Synthesis.Claims) == 0 || !citationsAreClosed(run.Synthesis) || len(run.Synthesis.RejectedClaims) > 0 || len(run.Synthesis.Conflicts) > 0 {
		return 0
	}
	return 1
}

func diagnosticSafetyScore(result diagnostic.Result) float64 {
	if len(result.Hypotheses) == 0 {
		return 0
	}
	for _, hypothesis := range result.Hypotheses {
		if len(hypothesis.Evidence) == 0 || len(hypothesis.VerificationSteps) == 0 {
			return 0
		}
		for _, step := range hypothesis.VerificationSteps {
			if !step.ReadOnly {
				return 0
			}
		}
	}
	return 1
}

func diagnosticHypothesisIDs(result diagnostic.Result) []string {
	ids := make([]string, 0, len(result.Hypotheses))
	for _, item := range result.Hypotheses {
		ids = append(ids, item.ID)
	}
	return ids
}

func collaborationClaimIDs(run orchestration.CollaborationRun) []string {
	if run.Synthesis == nil {
		return nil
	}
	ids := make([]string, 0, len(run.Synthesis.Claims))
	for _, item := range run.Synthesis.Claims {
		ids = append(ids, item.ID)
	}
	return ids
}

func collaborationEvidenceIDs(run orchestration.CollaborationRun) []string {
	if run.Synthesis == nil {
		return nil
	}
	ids := make([]string, 0, len(run.Synthesis.Evidence))
	for _, item := range run.Synthesis.Evidence {
		ids = append(ids, item.ID)
	}
	return ids
}

func rootCauseRecall(actual []string, expected []string) float64 { return setRecall(actual, expected) }
func evidenceRecall(actual []string, expected []string) float64  { return setRecall(actual, expected) }

func setRecall(actual []string, expected []string) float64 {
	if len(expected) == 0 {
		return 1
	}
	set := make(map[string]struct{}, len(actual))
	for _, value := range actual {
		set[strings.TrimSpace(value)] = struct{}{}
	}
	hits := 0
	for _, value := range expected {
		if _, exists := set[strings.TrimSpace(value)]; exists {
			hits++
		}
	}
	return float64(hits) / float64(len(expected))
}

func collaborationPercentile(values []float64, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64{}, values...)
	sort.Float64s(ordered)
	index := int(quantile*float64(len(ordered))+.999999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(ordered) {
		index = len(ordered) - 1
	}
	return ordered[index]
}

func pairedBootstrapCI(deltas []float64, iterations int) (float64, float64) {
	if len(deltas) == 0 || iterations < 1 {
		return 0, 0
	}
	random := rand.New(rand.NewSource(20260905))
	means := make([]float64, iterations)
	for iteration := range means {
		var total float64
		for range deltas {
			total += deltas[random.Intn(len(deltas))]
		}
		means[iteration] = total / float64(len(deltas))
	}
	sort.Float64s(means)
	return means[int(.025*float64(iterations))], means[int(.975*float64(iterations))-1]
}

func containsReason(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func validateCollaborationCase(item CollaborationCase) error {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Question) == "" || item.DatasetVersion != CollaborationDatasetVersion {
		return errors.New("case identity, question or dataset version is invalid")
	}
	if item.Slice != CollaborationSliceTarget && item.Slice != CollaborationSliceGuard {
		return errors.New("case slice is invalid")
	}
	if strings.TrimSpace(item.ReviewedBy) == "" {
		return errors.New("reviewed_by is required")
	}
	if item.Slice == CollaborationSliceTarget && (len(item.Expected.EvidenceIDs) == 0 || len(item.Expected.RootCauseIDs) == 0) {
		return errors.New("target case requires evidence and root-cause labels")
	}
	if item.Slice == CollaborationSliceGuard && (len(item.Expected.EvidenceIDs) != 0 || len(item.Expected.RootCauseIDs) != 0) {
		return errors.New("simple guard case must not carry target labels")
	}
	return nil
}

func WriteCollaborationReportMarkdown(writer io.Writer, report CollaborationABReport) error {
	if writer == nil {
		return errors.New("collaboration markdown writer is required")
	}
	_, err := fmt.Fprintf(writer, "# 有界多 Agent 成对 A/B 候选报告\n\n- 数据集：`%s`（复杂目标 %d 条，简单门禁 %d 条）\n- 候选版本：`%s`\n- 人工复核：`%t`；技术门：`%t`；可晋级：`%t`\n- 默认流量：`%d%%`（当前保持关闭）\n\n## 核心结果\n\n| 指标 | 结果 |\n| --- | ---: |\n| standard 平均质量 | %.4f |\n| collaborative 平均质量 | %.4f |\n| 成对质量提升 | %+.4f |\n| 质量提升 95%% CI | [%.4f, %.4f] |\n| standard / collaborative P95 | %.0fms / %.0fms |\n| 简单请求误触发率 | %.2f%% |\n| 最大实际 Agent 数 | %d |\n| 预算 / 安全违规 | %d / %d |\n\n> 本报告标签若仍为 `pending_user`，只证明候选实现可测，不可作为正式基线或自动切流依据。\n",
		report.DatasetVersion, report.Metrics.TargetCaseCount, report.Metrics.SimpleGuardCaseCount, report.CandidateVersion,
		report.HumanReviewed, report.TechnicalGatesPassed, report.PromotionEligible, report.RecommendedDefaultWeight,
		report.Metrics.BaselineMeanQuality, report.Metrics.CandidateMeanQuality, report.Metrics.MeanQualityDelta,
		report.Metrics.QualityDeltaCI95Lower, report.Metrics.QualityDeltaCI95Upper,
		report.Metrics.BaselineP95LatencyMS, report.Metrics.CandidateP95LatencyMS,
		report.Metrics.SimpleFalseTriggerRate*100, report.Metrics.MaximumObservedAgents,
		report.Metrics.BudgetViolationCount, report.Metrics.SafetyViolationCount)
	return err
}
