package evaluation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	knowledgeagent "GopherAI/internal/agent/knowledge"
)

const (
	ParentContextEvaluatorVersion = "parent-context-paired-evaluator-v1"
	ParentContextDatasetVersion   = "devsupport-parent-context-ab-v1"
	ParentContextCaseCount        = 20
	ParentContextTargetCaseCount  = 10
	ParentContextGuardCaseCount   = 10

	ParentContextSliceTarget = "parent_context_target"
	ParentContextSliceGuard  = "regression_guard"
)

type ParentContextExpected struct {
	EvidenceIDs   []string `json:"evidence_ids"`
	AnswerFacts   []string `json:"answer_facts"`
	ShouldResolve bool     `json:"should_resolve"`
}

type ParentContextCase struct {
	ID             string                `json:"id"`
	Slice          string                `json:"slice"`
	Question       string                `json:"question"`
	Expected       ParentContextExpected `json:"expected"`
	Tags           []string              `json:"tags"`
	ReviewedBy     string                `json:"reviewed_by"`
	DatasetVersion string                `json:"dataset_version"`
}

type ParentContextCaseResult struct {
	ID                         string   `json:"id"`
	Slice                      string   `json:"slice"`
	BaselineQuality            float64  `json:"baseline_quality"`
	CandidateQuality           float64  `json:"candidate_quality"`
	QualityDelta               float64  `json:"quality_delta"`
	BaselineDocumentDiversity  int      `json:"baseline_document_diversity"`
	CandidateDocumentDiversity int      `json:"candidate_document_diversity"`
	DocumentDiversityDelta     float64  `json:"document_diversity_delta"`
	BaselineLatencyMS          int64    `json:"baseline_latency_ms"`
	CandidateLatencyMS         int64    `json:"candidate_latency_ms"`
	BaselineInputTokens        int      `json:"baseline_input_tokens"`
	CandidateInputTokens       int      `json:"candidate_input_tokens"`
	BaselineOutputTokens       int      `json:"baseline_output_tokens"`
	CandidateOutputTokens      int      `json:"candidate_output_tokens"`
	ParentContextHits          int      `json:"parent_context_hits"`
	CandidateChildCitationOnly bool     `json:"candidate_child_citation_only"`
	BaselineResolved           bool     `json:"baseline_resolved"`
	CandidateResolved          bool     `json:"candidate_resolved"`
	BaselineAnswer             string   `json:"baseline_answer,omitempty"`
	CandidateAnswer            string   `json:"candidate_answer,omitempty"`
	SafetyPassed               bool     `json:"safety_passed"`
	ReasonCodes                []string `json:"reason_codes,omitempty"`
	Error                      string   `json:"error,omitempty"`
}

type ParentContextABMetrics struct {
	CaseCount                       int     `json:"case_count"`
	TargetCaseCount                 int     `json:"target_case_count"`
	GuardCaseCount                  int     `json:"guard_case_count"`
	BaselineMeanQuality             float64 `json:"baseline_mean_quality"`
	CandidateMeanQuality            float64 `json:"candidate_mean_quality"`
	TargetMeanQualityDelta          float64 `json:"target_mean_quality_delta"`
	TargetQualityDeltaCI95Lower     float64 `json:"target_quality_delta_ci95_lower"`
	TargetQualityDeltaCI95Upper     float64 `json:"target_quality_delta_ci95_upper"`
	GuardMeanQualityDelta           float64 `json:"guard_mean_quality_delta"`
	BaselineMeanDocumentDiversity   float64 `json:"baseline_mean_document_diversity"`
	CandidateMeanDocumentDiversity  float64 `json:"candidate_mean_document_diversity"`
	MeanDocumentDiversityDelta      float64 `json:"mean_document_diversity_delta"`
	DiversityDeltaCI95Lower         float64 `json:"diversity_delta_ci95_lower"`
	DiversityDeltaCI95Upper         float64 `json:"diversity_delta_ci95_upper"`
	BaselineMeanInputTokens         float64 `json:"baseline_mean_input_tokens"`
	CandidateMeanInputTokens        float64 `json:"candidate_mean_input_tokens"`
	BaselineMeanOutputTokens        float64 `json:"baseline_mean_output_tokens"`
	CandidateMeanOutputTokens       float64 `json:"candidate_mean_output_tokens"`
	InputTokenOverheadRate          float64 `json:"input_token_overhead_rate"`
	BaselineP95LatencyMS            float64 `json:"baseline_p95_latency_ms"`
	CandidateP95LatencyMS           float64 `json:"candidate_p95_latency_ms"`
	BaselineP99LatencyMS            float64 `json:"baseline_p99_latency_ms"`
	CandidateP99LatencyMS           float64 `json:"candidate_p99_latency_ms"`
	TargetParentContextAvailability float64 `json:"target_parent_context_availability"`
	ChildCitationIntegrityRate      float64 `json:"child_citation_integrity_rate"`
	SafetyViolationCount            int     `json:"safety_violation_count"`
	CandidateExecutionFailureCount  int     `json:"candidate_execution_failure_count"`
	BaselineExecutionFailureCount   int     `json:"baseline_execution_failure_count"`
}

type ParentContextRuntime struct {
	DatasetSHA256        string `json:"dataset_sha256,omitempty"`
	FixtureSHA256        string `json:"fixture_sha256,omitempty"`
	BaselineStrategy     string `json:"baseline_strategy"`
	CandidateStrategy    string `json:"candidate_strategy"`
	EmbeddingModel       string `json:"embedding_model,omitempty"`
	ChatModel            string `json:"chat_model,omitempty"`
	Environment          string `json:"environment,omitempty"`
	ExternalModelMutable bool   `json:"external_model_mutable"`
}

type ParentContextABReport struct {
	SchemaVersion            string                    `json:"schema_version"`
	EvaluatorVersion         string                    `json:"evaluator_version"`
	DatasetVersion           string                    `json:"dataset_version"`
	CandidateVersion         string                    `json:"candidate_version"`
	GeneratedAt              time.Time                 `json:"generated_at"`
	HumanReviewed            bool                      `json:"human_reviewed"`
	TechnicalGatesPassed     bool                      `json:"technical_gates_passed"`
	NetBenefitPassed         bool                      `json:"net_benefit_passed"`
	PromotionEligible        bool                      `json:"promotion_eligible"`
	RecommendedDefaultWeight int                       `json:"recommended_default_weight"`
	GateFailures             []string                  `json:"gate_failures,omitempty"`
	MetricNotes              map[string]string         `json:"metric_notes"`
	Metrics                  ParentContextABMetrics    `json:"metrics"`
	Runtime                  ParentContextRuntime      `json:"runtime"`
	Cases                    []ParentContextCaseResult `json:"cases"`
}

func LoadParentContextCases(reader io.Reader) ([]ParentContextCase, error) {
	if reader == nil {
		return nil, errors.New("parent-context dataset reader is required")
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	cases := make([]ParentContextCase, 0, ParentContextCaseCount)
	seen := make(map[string]struct{}, ParentContextCaseCount)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var item ParentContextCase
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&item); err != nil {
			return nil, fmt.Errorf("decode parent-context dataset line %d: %w", line, err)
		}
		if err := validateParentContextCase(item); err != nil {
			return nil, fmt.Errorf("parent-context dataset line %d: %w", line, err)
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return nil, fmt.Errorf("parent-context dataset line %d: duplicate id %s", line, item.ID)
		}
		seen[item.ID] = struct{}{}
		cases = append(cases, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(cases) != ParentContextCaseCount {
		return nil, fmt.Errorf("parent-context dataset must contain %d cases, got %d", ParentContextCaseCount, len(cases))
	}
	targets, guards := 0, 0
	for _, item := range cases {
		if item.Slice == ParentContextSliceTarget {
			targets++
		} else {
			guards++
		}
	}
	if targets != ParentContextTargetCaseCount || guards != ParentContextGuardCaseCount {
		return nil, fmt.Errorf("parent-context dataset requires %d target and %d guard cases, got %d and %d", ParentContextTargetCaseCount, ParentContextGuardCaseCount, targets, guards)
	}
	return cases, nil
}

func EvaluateParentContextAB(ctx context.Context, cases []ParentContextCase, tenantID string, userID string, candidateVersion string, generatedAt time.Time, baseline RAGAnswerer, candidate RAGAnswerer) (ParentContextABReport, error) {
	if baseline == nil || candidate == nil {
		return ParentContextABReport{}, errors.New("baseline and parent-context answerers are required")
	}
	if len(cases) != ParentContextCaseCount {
		return ParentContextABReport{}, fmt.Errorf("parent-context evaluation requires %d cases", ParentContextCaseCount)
	}
	report := ParentContextABReport{
		SchemaVersion: "parent-context-paired-ab-v1", EvaluatorVersion: ParentContextEvaluatorVersion,
		DatasetVersion: ParentContextDatasetVersion, CandidateVersion: strings.TrimSpace(candidateVersion), GeneratedAt: generatedAt.UTC(),
		HumanReviewed: true, RecommendedDefaultWeight: 0, Cases: make([]ParentContextCaseResult, 0, len(cases)),
		MetricNotes: map[string]string{
			"quality":             "同题同模型成对比较；事实召回 55%、引用精度 20%、引用覆盖 15%、正确解决状态 10%。",
			"diversity":           "每题回答证据中不同 document_id 数量；候选在 Parent 与文档占位上限后统计。",
			"confidence_interval": "目标质量差与全量文档多样性差均使用 2000 次固定随机种子 paired bootstrap 95% CI。",
			"promotion":           "技术门与净收益门分离；pending_user 标签、可变外部模型或未跑密封留出集时默认权重始终为 0。",
		},
	}
	for _, item := range cases {
		if item.ReviewedBy != "human" {
			report.HumanReviewed = false
		}
		report.Cases = append(report.Cases, evaluateParentContextCase(ctx, item, strings.TrimSpace(tenantID), strings.TrimSpace(userID), baseline, candidate))
	}
	report.Metrics = aggregateParentContextMetrics(report.Cases)
	report.GateFailures = parentContextGateFailures(report.Metrics)
	report.TechnicalGatesPassed = len(report.GateFailures) == 0
	latencyBudget := math.Max(report.Metrics.BaselineP99LatencyMS*1.8, report.Metrics.BaselineP99LatencyMS+2000)
	report.NetBenefitPassed = report.Metrics.TargetMeanQualityDelta >= .03 && report.Metrics.TargetQualityDeltaCI95Lower >= 0 &&
		report.Metrics.MeanDocumentDiversityDelta >= 0 && report.Metrics.CandidateP99LatencyMS <= latencyBudget &&
		report.Metrics.InputTokenOverheadRate <= .50 && report.Metrics.SafetyViolationCount == 0
	report.PromotionEligible = report.HumanReviewed && report.TechnicalGatesPassed && report.NetBenefitPassed
	return report, nil
}

func evaluateParentContextCase(ctx context.Context, item ParentContextCase, tenantID string, userID string, baseline RAGAnswerer, candidate RAGAnswerer) ParentContextCaseResult {
	result := ParentContextCaseResult{ID: item.ID, Slice: item.Slice, SafetyPassed: true}
	input := knowledgeagent.Input{TenantID: tenantID, UserID: userID, Question: item.Question, TopK: 5}
	baselineStarted := time.Now()
	baselineOutput, baselineErr := baseline.Answer(ctx, input)
	result.BaselineLatencyMS = time.Since(baselineStarted).Milliseconds()
	candidateStarted := time.Now()
	candidateOutput, candidateErr := candidate.Answer(ctx, input)
	result.CandidateLatencyMS = time.Since(candidateStarted).Milliseconds()
	if baselineErr != nil {
		result.Error = "baseline: " + baselineErr.Error()
	} else {
		result.BaselineQuality = parentAnswerQuality(baselineOutput, item.Expected)
		result.BaselineDocumentDiversity = documentDiversity(baselineOutput)
		result.BaselineInputTokens = baselineOutput.Result.Usage.InputTokens
		result.BaselineOutputTokens = baselineOutput.Result.Usage.OutputTokens
		result.BaselineResolved = baselineOutput.Result.Resolved
		result.BaselineAnswer = baselineOutput.Result.Answer
	}
	if candidateErr != nil {
		if result.Error != "" {
			result.Error += "; "
		}
		result.Error += "candidate: " + candidateErr.Error()
		return result
	}
	result.CandidateQuality = parentAnswerQuality(candidateOutput, item.Expected)
	result.CandidateDocumentDiversity = documentDiversity(candidateOutput)
	result.CandidateInputTokens = candidateOutput.Result.Usage.InputTokens
	result.CandidateOutputTokens = candidateOutput.Result.Usage.OutputTokens
	result.CandidateResolved = candidateOutput.Result.Resolved
	result.CandidateAnswer = candidateOutput.Result.Answer
	if candidateOutput.Diagnostics.Parent != nil {
		result.ParentContextHits = candidateOutput.Diagnostics.Parent.ParentContextHits
		result.CandidateChildCitationOnly = candidateOutput.Diagnostics.Parent.ChildCitationOnly
	}
	result.SafetyPassed, result.ReasonCodes = parentContextSafety(candidateOutput, tenantID)
	result.QualityDelta = result.CandidateQuality - result.BaselineQuality
	result.DocumentDiversityDelta = float64(result.CandidateDocumentDiversity - result.BaselineDocumentDiversity)
	return result
}

func parentAnswerQuality(output knowledgeagent.Output, expected ParentContextExpected) float64 {
	if !expected.ShouldResolve {
		if !output.Result.Resolved && output.Result.NeedsUserInput {
			return 1
		}
		return 0
	}
	factRecall := textFactRecall(output.Result.Answer, expected.AnswerFacts)
	citationPrecision := citationSetPrecision(output, expected.EvidenceIDs)
	citationCoverage := 0.0
	if containsAll(citationEvidenceIDs(output), expected.EvidenceIDs) {
		citationCoverage = 1
	}
	resolved := 0.0
	if output.Result.Resolved {
		resolved = 1
	}
	return .55*factRecall + .20*citationPrecision + .15*citationCoverage + .10*resolved
}

func textFactRecall(answer string, facts []string) float64 {
	if len(facts) == 0 {
		return 1
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(answer), ""))
	hits := 0
	for _, fact := range facts {
		if strings.Contains(normalized, strings.ToLower(strings.Join(strings.Fields(fact), ""))) {
			hits++
		}
	}
	return float64(hits) / float64(len(facts))
}

func citationSetPrecision(output knowledgeagent.Output, expected []string) float64 {
	ids := citationEvidenceIDs(output)
	if len(ids) == 0 {
		return 0
	}
	hits := 0
	for _, id := range ids {
		if contains(expected, id) {
			hits++
		}
	}
	return float64(hits) / float64(len(ids))
}

func citationEvidenceIDs(output knowledgeagent.Output) []string {
	ids := make([]string, 0, len(output.Result.Citations))
	for _, item := range output.Result.Citations {
		ids = append(ids, item.EvidenceID)
	}
	return ids
}

func documentDiversity(output knowledgeagent.Output) int {
	documents := make(map[string]struct{})
	for _, item := range output.Result.Evidence {
		if sourceID := strings.TrimSpace(item.SourceID); sourceID != "" {
			documents[sourceID] = struct{}{}
		}
	}
	return len(documents)
}

func parentContextSafety(output knowledgeagent.Output, tenantID string) (bool, []string) {
	reasons := []string{}
	evidence := make(map[string]struct{}, len(output.Result.Evidence))
	parents := make(map[string]struct{}, len(output.Result.Evidence))
	for _, item := range output.Result.Evidence {
		if item.TenantID != tenantID || strings.TrimSpace(item.SourceID) == "" {
			reasons = append(reasons, "evidence_acl_or_identity_failed")
		}
		evidence[item.ID] = struct{}{}
		if item.ParentEvidenceID != "" {
			parents[item.ParentEvidenceID] = struct{}{}
		}
	}
	for _, citation := range output.Result.Citations {
		if _, exists := evidence[citation.EvidenceID]; !exists {
			reasons = append(reasons, "citation_not_in_child_evidence")
		}
		if _, isParent := parents[citation.EvidenceID]; isParent {
			reasons = append(reasons, "parent_used_as_citation")
		}
	}
	if output.Diagnostics.Parent == nil || !output.Diagnostics.Parent.ChildCitationOnly {
		reasons = append(reasons, "child_citation_contract_missing")
	}
	return len(reasons) == 0, uniqueStrings(reasons)
}

func aggregateParentContextMetrics(cases []ParentContextCaseResult) ParentContextABMetrics {
	metrics := ParentContextABMetrics{CaseCount: len(cases)}
	baselineLatencies, candidateLatencies := []float64{}, []float64{}
	qualityTargetDeltas, qualityGuardDeltas, diversityDeltas := []float64{}, []float64{}, []float64{}
	var baselineQuality, candidateQuality, baselineDiversity, candidateDiversity float64
	var baselineInput, candidateInput, baselineOutput, candidateOutput int
	parentAvailable, childCitationSafe := 0, 0
	for _, item := range cases {
		baselineQuality += item.BaselineQuality
		candidateQuality += item.CandidateQuality
		baselineDiversity += float64(item.BaselineDocumentDiversity)
		candidateDiversity += float64(item.CandidateDocumentDiversity)
		baselineInput += item.BaselineInputTokens
		candidateInput += item.CandidateInputTokens
		baselineOutput += item.BaselineOutputTokens
		candidateOutput += item.CandidateOutputTokens
		baselineLatencies = append(baselineLatencies, float64(item.BaselineLatencyMS))
		candidateLatencies = append(candidateLatencies, float64(item.CandidateLatencyMS))
		diversityDeltas = append(diversityDeltas, item.DocumentDiversityDelta)
		if item.Slice == ParentContextSliceTarget {
			metrics.TargetCaseCount++
			qualityTargetDeltas = append(qualityTargetDeltas, item.QualityDelta)
			if item.ParentContextHits > 0 {
				parentAvailable++
			}
		} else {
			metrics.GuardCaseCount++
			qualityGuardDeltas = append(qualityGuardDeltas, item.QualityDelta)
		}
		if item.CandidateChildCitationOnly {
			childCitationSafe++
		}
		if !item.SafetyPassed {
			metrics.SafetyViolationCount++
		}
		if strings.Contains(item.Error, "candidate:") {
			metrics.CandidateExecutionFailureCount++
		}
		if strings.Contains(item.Error, "baseline:") {
			metrics.BaselineExecutionFailureCount++
		}
	}
	count := float64(len(cases))
	if count > 0 {
		metrics.BaselineMeanQuality = baselineQuality / count
		metrics.CandidateMeanQuality = candidateQuality / count
		metrics.BaselineMeanDocumentDiversity = baselineDiversity / count
		metrics.CandidateMeanDocumentDiversity = candidateDiversity / count
		metrics.MeanDocumentDiversityDelta = meanFloat64(diversityDeltas)
		metrics.BaselineMeanInputTokens = float64(baselineInput) / count
		metrics.CandidateMeanInputTokens = float64(candidateInput) / count
		metrics.BaselineMeanOutputTokens = float64(baselineOutput) / count
		metrics.CandidateMeanOutputTokens = float64(candidateOutput) / count
		metrics.ChildCitationIntegrityRate = float64(childCitationSafe) / count
	}
	metrics.TargetMeanQualityDelta = meanFloat64(qualityTargetDeltas)
	metrics.GuardMeanQualityDelta = meanFloat64(qualityGuardDeltas)
	metrics.TargetQualityDeltaCI95Lower, metrics.TargetQualityDeltaCI95Upper = pairedBootstrapCI(qualityTargetDeltas, 2000)
	metrics.DiversityDeltaCI95Lower, metrics.DiversityDeltaCI95Upper = pairedBootstrapCI(diversityDeltas, 2000)
	metrics.InputTokenOverheadRate = ratioDelta(metrics.CandidateMeanInputTokens, metrics.BaselineMeanInputTokens)
	metrics.BaselineP95LatencyMS, metrics.BaselineP99LatencyMS = parentABPercentile(baselineLatencies, .95), parentABPercentile(baselineLatencies, .99)
	metrics.CandidateP95LatencyMS, metrics.CandidateP99LatencyMS = parentABPercentile(candidateLatencies, .95), parentABPercentile(candidateLatencies, .99)
	if metrics.TargetCaseCount > 0 {
		metrics.TargetParentContextAvailability = float64(parentAvailable) / float64(metrics.TargetCaseCount)
	}
	return metrics
}

func parentContextGateFailures(metrics ParentContextABMetrics) []string {
	failures := []string{}
	if metrics.TargetCaseCount != ParentContextTargetCaseCount || metrics.GuardCaseCount != ParentContextGuardCaseCount {
		failures = append(failures, "dataset_slice_count_invalid")
	}
	if metrics.BaselineExecutionFailureCount != 0 || metrics.CandidateExecutionFailureCount != 0 {
		failures = append(failures, "execution_failure_nonzero")
	}
	if metrics.SafetyViolationCount != 0 || metrics.ChildCitationIntegrityRate != 1 {
		failures = append(failures, "child_citation_integrity_failed")
	}
	if metrics.TargetParentContextAvailability < .8 {
		failures = append(failures, "target_parent_context_availability_below_0.80")
	}
	if metrics.GuardMeanQualityDelta < -.02 {
		failures = append(failures, "guard_quality_regression_below_-0.02")
	}
	return failures
}

func validateParentContextCase(item ParentContextCase) error {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Question) == "" || item.DatasetVersion != ParentContextDatasetVersion {
		return errors.New("case identity, question or dataset version is invalid")
	}
	if item.Slice != ParentContextSliceTarget && item.Slice != ParentContextSliceGuard {
		return errors.New("case slice is invalid")
	}
	if item.ReviewedBy != "human" && item.ReviewedBy != "pending_user" {
		return errors.New("reviewed_by must be human or pending_user")
	}
	if !item.Expected.ShouldResolve || len(item.Expected.EvidenceIDs) == 0 || len(item.Expected.AnswerFacts) == 0 {
		return errors.New("paired parent-context case requires resolvable evidence and answer facts")
	}
	return nil
}

func meanFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func ratioDelta(candidate float64, baseline float64) float64 {
	if baseline == 0 {
		if candidate == 0 {
			return 0
		}
		return 1
	}
	return (candidate - baseline) / baseline
}

func parentABPercentile(values []float64, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64{}, values...)
	sort.Float64s(ordered)
	index := int(math.Ceil(float64(len(ordered))*quantile)) - 1
	if index < 0 {
		index = 0
	}
	return ordered[min(index, len(ordered)-1)]
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func WriteParentContextReportMarkdown(writer io.Writer, report ParentContextABReport) error {
	if writer == nil {
		return errors.New("parent-context markdown writer is required")
	}
	_, err := fmt.Fprintf(writer, "# 父子上下文成对 A/B 候选报告\n\n- 数据集：`%s`（目标 %d 条，回归门禁 %d 条）\n- 候选版本：`%s`\n- 人工复核：`%t`；技术门：`%t`；净收益门：`%t`；可晋级：`%t`\n- 推荐默认权重：`%d%%`\n\n| 指标 | rag_fast | parent-context / 差值 |\n| --- | ---: | ---: |\n| 平均质量 | %.4f | %.4f |\n| 目标切片质量差及 95%% CI | - | %+.4f [%.4f, %.4f] |\n| 平均文档多样性 | %.2f | %.2f（%+.2f） |\n| 多样性差 95%% CI | - | [%.2f, %.2f] |\n| 平均输入 Token | %.0f | %.0f（%+.2f%%） |\n| P95 延迟 | %.0fms | %.0fms |\n| P99 延迟 | %.0fms | %.0fms |\n| Child 引用完整率 | - | %.2f%% |\n\n> 标签若仍为 `pending_user`，报告只证明候选可测；外部模型可变，不能据此自动切流。\n",
		report.DatasetVersion, report.Metrics.TargetCaseCount, report.Metrics.GuardCaseCount, report.CandidateVersion,
		report.HumanReviewed, report.TechnicalGatesPassed, report.NetBenefitPassed, report.PromotionEligible, report.RecommendedDefaultWeight,
		report.Metrics.BaselineMeanQuality, report.Metrics.CandidateMeanQuality,
		report.Metrics.TargetMeanQualityDelta, report.Metrics.TargetQualityDeltaCI95Lower, report.Metrics.TargetQualityDeltaCI95Upper,
		report.Metrics.BaselineMeanDocumentDiversity, report.Metrics.CandidateMeanDocumentDiversity, report.Metrics.MeanDocumentDiversityDelta,
		report.Metrics.DiversityDeltaCI95Lower, report.Metrics.DiversityDeltaCI95Upper,
		report.Metrics.BaselineMeanInputTokens, report.Metrics.CandidateMeanInputTokens, report.Metrics.InputTokenOverheadRate*100,
		report.Metrics.BaselineP95LatencyMS, report.Metrics.CandidateP95LatencyMS,
		report.Metrics.BaselineP99LatencyMS, report.Metrics.CandidateP99LatencyMS,
		report.Metrics.ChildCitationIntegrityRate*100)
	return err
}
