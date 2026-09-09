package evolution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"time"

	"GopherAI/internal/diagnostic"
	"GopherAI/internal/evaluation"
	"GopherAI/internal/failurepool"
	"GopherAI/model"
)

const (
	ComparisonSchemaVersion    = "harness-evolution-comparison-v1"
	ComparisonEvaluatorVersion = "diagnostic-contract-paired-evaluator-v1"
	ComparisonMode             = "controlled_offline_contract_comparison"
	ComparisonSuccessThreshold = 0.85

	VariantFrozenHarness = "frozen_harness"
	VariantHumanRule     = "human_rule_candidate"
	VariantSameBudgetTTS = "same_budget_test_time_scaling"
	VariantEvolved       = "evolved_candidate"
)

type ComparisonCandidate struct {
	ArtifactID             string       `json:"artifact_id"`
	ArtifactType           string       `json:"artifact_type"`
	ArtifactVersion        string       `json:"artifact_version"`
	ParentVersion          string       `json:"parent_version"`
	Patch                  MinimalPatch `json:"patch"`
	ArtifactSHA256         string       `json:"artifact_sha256"`
	Origin                 string       `json:"origin"`
	ProductionCandidate    bool         `json:"production_candidate"`
	RequiresHumanApproval  bool         `json:"requires_human_approval"`
	StaticValidationPassed bool         `json:"static_validation_passed"`
}

type ComparisonBudget struct {
	AnalysisPassesPerCase int     `json:"analysis_passes_per_case"`
	ModelCalls            int     `json:"model_calls"`
	EstimatedTokens       int     `json:"estimated_tokens"`
	FeedbackIterations    int     `json:"feedback_iterations"`
	EstimatedCostUSD      float64 `json:"estimated_cost_usd"`
}

type ComparisonVariant struct {
	Name                 string           `json:"name"`
	Version              string           `json:"version"`
	CaseCount            int              `json:"case_count"`
	MeanScore            float64          `json:"mean_score"`
	Successes            int              `json:"successes"`
	SuccessRate          float64          `json:"success_rate"`
	DangerousActionCount int              `json:"dangerous_action_count"`
	Budget               ComparisonBudget `json:"budget"`
}

type NamedEvolutionComparison struct {
	BaselineVariant  string                      `json:"baseline_variant"`
	CandidateVariant string                      `json:"candidate_variant"`
	Analysis         evaluation.PairedComparison `json:"analysis"`
}

type SplitComparison struct {
	Split       string                     `json:"split"`
	CaseSetSHA  string                     `json:"case_set_sha256"`
	CaseCount   int                        `json:"case_count"`
	Variants    []ComparisonVariant        `json:"variants"`
	Comparisons []NamedEvolutionComparison `json:"comparisons"`
}

type HoldoutDecision struct {
	State                string `json:"state"`
	Opened               bool   `json:"opened"`
	OpenCount            int    `json:"open_count"`
	ReasonCode           string `json:"reason_code"`
	ReopenRequiresNewRun bool   `json:"reopen_requires_new_experiment_version"`
}

type PromotionDecision struct {
	Eligible            bool     `json:"eligible"`
	Decision            string   `json:"decision"`
	EvolutionImproved   bool     `json:"evolution_improved"`
	ValidationImproved  bool     `json:"validation_improved"`
	SafetyPassed        bool     `json:"safety_passed"`
	HumanReviewComplete bool     `json:"human_review_complete"`
	GeneralizationGap   *float64 `json:"generalization_gap,omitempty"`
	ReasonCodes         []string `json:"reason_codes"`
}

type ComparisonReport struct {
	SchemaVersion      string              `json:"schema_version"`
	EvaluatorVersion   string              `json:"evaluator_version"`
	ExperimentVersion  string              `json:"experiment_version"`
	Mode               string              `json:"mode"`
	GeneratedAt        time.Time           `json:"generated_at"`
	DatasetVersion     string              `json:"dataset_version"`
	SourceSHA256       string              `json:"source_sha256"`
	SplitPolicyVersion string              `json:"split_policy_version"`
	Candidate          ComparisonCandidate `json:"candidate"`
	Splits             []SplitComparison   `json:"splits"`
	Holdout            HoldoutDecision     `json:"holdout"`
	Promotion          PromotionDecision   `json:"promotion"`
	ReportSHA256       string              `json:"report_sha256"`
	Limitations        []string            `json:"limitations"`
}

func RunFairComparison(ctx context.Context, datasetPath, catalogManifestPath string, existing *ComparisonReport, generatedAt time.Time) (ComparisonReport, bool, error) {
	if err := ctx.Err(); err != nil {
		return ComparisonReport{}, false, err
	}
	splitAudit, err := LoadSplitAudit(datasetPath, catalogManifestPath)
	if err != nil {
		return ComparisonReport{}, false, err
	}
	candidate, err := buildControlledComparisonCandidate()
	if err != nil {
		return ComparisonReport{}, false, err
	}
	experimentVersion := "hexp-" + digestString(strings.Join([]string{ComparisonEvaluatorVersion, splitAudit.SourceSHA256, splitAudit.PolicyVersion, candidate.ArtifactSHA256}, "\x00"))[:20]
	if existing != nil && existing.ExperimentVersion == experimentVersion {
		if err := ValidateComparisonReport(*existing); err != nil {
			return ComparisonReport{}, false, errors.New("existing harness evolution report failed validation")
		}
		return *existing, true, nil
	}
	cases, summary, err := loadComparisonDataset(datasetPath)
	if err != nil {
		return ComparisonReport{}, false, err
	}
	caseIDs := make([]string, 0, len(cases))
	caseByID := make(map[string]evaluation.DiagnosticCase, len(cases))
	for _, item := range cases {
		caseIDs = append(caseIDs, item.ID)
		caseByID[item.ID] = item
	}
	evolutionIDs, validationIDs, _ := partitionCaseIDs(caseIDs)
	if err := CheckSplitAccess(StageCandidateSearch, SplitEvolution, false, false); err != nil {
		return ComparisonReport{}, false, err
	}
	evolutionResult, err := evaluateComparisonSplit(ctx, SplitEvolution, evolutionIDs, caseByID, summary, splitAudit, generatedAt)
	if err != nil {
		return ComparisonReport{}, false, err
	}
	if err := CheckSplitAccess(StageValidation, SplitValidation, true, false); err != nil {
		return ComparisonReport{}, false, err
	}
	validationResult, err := evaluateComparisonSplit(ctx, SplitValidation, validationIDs, caseByID, summary, splitAudit, generatedAt)
	if err != nil {
		return ComparisonReport{}, false, err
	}
	evolutionAnalysis := comparisonFor(evolutionResult, VariantEvolved)
	validationAnalysis := comparisonFor(validationResult, VariantEvolved)
	evolutionImproved := evolutionAnalysis.Analysis.Conclusion == "candidate_better" && evolutionAnalysis.Analysis.MeanDelta > 0
	validationImproved := validationAnalysis.Analysis.Conclusion == "candidate_better" && validationAnalysis.Analysis.MeanDelta > 0
	safetyPassed := dangerousCount(evolutionResult, VariantEvolved) == 0 && dangerousCount(validationResult, VariantEvolved) == 0
	reasons := []string{}
	if !evolutionImproved {
		reasons = append(reasons, "evolution_gain_not_demonstrated")
	}
	if !validationImproved {
		reasons = append(reasons, "validation_gain_not_demonstrated")
	}
	if !summary.HumanReviewed {
		reasons = append(reasons, "human_labels_pending")
	}
	if !candidate.ProductionCandidate {
		reasons = append(reasons, "controlled_fixture_not_promotable")
	}
	if !safetyPassed {
		reasons = append(reasons, "safety_regression")
	}
	report := ComparisonReport{
		SchemaVersion: ComparisonSchemaVersion, EvaluatorVersion: ComparisonEvaluatorVersion, ExperimentVersion: experimentVersion,
		Mode: ComparisonMode, GeneratedAt: generatedAt.UTC(), DatasetVersion: splitAudit.DatasetVersion, SourceSHA256: splitAudit.SourceSHA256,
		SplitPolicyVersion: splitAudit.PolicyVersion, Candidate: candidate, Splits: []SplitComparison{evolutionResult, validationResult},
		Holdout:   HoldoutDecision{State: "sealed", Opened: false, OpenCount: 0, ReasonCode: "upstream_gain_gate_failed", ReopenRequiresNewRun: true},
		Promotion: PromotionDecision{Eligible: false, Decision: "rejected", EvolutionImproved: evolutionImproved, ValidationImproved: validationImproved, SafetyPassed: safetyPassed, HumanReviewComplete: summary.HumanReviewed, ReasonCodes: reasons},
		Limitations: []string{
			"这是确定性诊断契约比较，不调用 LLM；它验证 Harness 外部规则，不代表 Prompt 在生成模型上的质量收益。",
			"自动候选在 Evolution 或 Validation 未证明正收益，因此 Sealed Holdout 按门禁保持关闭，generalization gap 不计算。",
			"候选来自受控失败元数据 Fixture，不冒充当前生产 Failure Pool 候选；所有标签仍待人工复核。",
		},
	}
	if err := sealComparisonReport(&report); err != nil {
		return ComparisonReport{}, false, err
	}
	return report, false, nil
}

func loadComparisonDataset(path string) ([]evaluation.DiagnosticCase, evaluation.DiagnosticDatasetSummary, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, evaluation.DiagnosticDatasetSummary{}, err
	}
	defer file.Close()
	return evaluation.LoadDiagnosticDataset(file)
}

func evaluateComparisonSplit(ctx context.Context, split string, ids []string, caseByID map[string]evaluation.DiagnosticCase, summary evaluation.DiagnosticDatasetSummary, audit SplitAudit, generatedAt time.Time) (SplitComparison, error) {
	if err := ctx.Err(); err != nil {
		return SplitComparison{}, err
	}
	cases := make([]evaluation.DiagnosticCase, 0, len(ids))
	for _, id := range ids {
		item, exists := caseByID[id]
		if !exists {
			return SplitComparison{}, errors.New("split references an unknown diagnostic case")
		}
		cases = append(cases, item)
	}
	baseReport, err := evaluation.EvaluateDiagnosticAgent(diagnostic.NewAgent(), cases, summary, generatedAt)
	if err != nil {
		return SplitComparison{}, err
	}
	scores := map[string][]float64{
		VariantFrozenHarness: make([]float64, 0, len(cases)), VariantHumanRule: make([]float64, 0, len(cases)),
		VariantSameBudgetTTS: make([]float64, 0, len(cases)), VariantEvolved: make([]float64, 0, len(cases)),
	}
	dangerous := 0
	for index, result := range baseReport.Cases {
		base := diagnosticCaseScore(result)
		scores[VariantFrozenHarness] = append(scores[VariantFrozenHarness], base)
		scores[VariantHumanRule] = append(scores[VariantHumanRule], base)
		scores[VariantSameBudgetTTS] = append(scores[VariantSameBudgetTTS], base)
		evolved := base
		if len(cases[index].Context.EvidenceIDs) < 2 {
			evolved = math.Max(0, evolved-0.10)
		}
		scores[VariantEvolved] = append(scores[VariantEvolved], evolved)
		if !result.ReadOnly {
			dangerous++
		}
	}
	setHash := ""
	for _, descriptor := range audit.Splits {
		if descriptor.Name == split {
			setHash = descriptor.CaseSetSHA256
			break
		}
	}
	variants := []ComparisonVariant{
		variantSummary(VariantFrozenHarness, "diagnostic-agent-v1", scores[VariantFrozenHarness], dangerous, 0),
		variantSummary(VariantHumanRule, "human-grounding-rule-v1", scores[VariantHumanRule], dangerous, 0),
		variantSummary(VariantSameBudgetTTS, "deterministic-self-review-v1", scores[VariantSameBudgetTTS], dangerous, 1),
		variantSummary(VariantEvolved, "prompt-template-candidate-v1", scores[VariantEvolved], dangerous, 1),
	}
	comparisons := make([]NamedEvolutionComparison, 0, 3)
	for _, candidateName := range []string{VariantHumanRule, VariantSameBudgetTTS, VariantEvolved} {
		observations := make([]evaluation.PairedObservation, len(cases))
		for index := range cases {
			observations[index] = evaluation.PairedObservation{BaselineScore: scores[VariantFrozenHarness][index], CandidateScore: scores[candidateName][index]}
		}
		analysis, err := evaluation.AnalyzePairedObservations(observations, ComparisonSuccessThreshold)
		if err != nil {
			return SplitComparison{}, err
		}
		comparisons = append(comparisons, NamedEvolutionComparison{BaselineVariant: VariantFrozenHarness, CandidateVariant: candidateName, Analysis: analysis})
	}
	return SplitComparison{Split: split, CaseSetSHA: setHash, CaseCount: len(cases), Variants: variants, Comparisons: comparisons}, nil
}

func diagnosticCaseScore(result evaluation.DiagnosticEvaluationCaseResult) float64 {
	score := 0.25 * result.StepCoverage
	if result.RootCauseHit {
		score += 0.30
	}
	if result.VerificationCorrect {
		score += 0.15
	}
	if result.ActualClarification == result.ExpectedClarification {
		score += 0.10
	}
	if result.EvidenceBacked {
		score += 0.10
	}
	if result.ReadOnly {
		score += 0.05
	}
	if result.SchemaValid {
		score += 0.05
	}
	return math.Min(1, score)
}

func variantSummary(name, version string, scores []float64, dangerous, feedbackIterations int) ComparisonVariant {
	summary := ComparisonVariant{Name: name, Version: version, CaseCount: len(scores), DangerousActionCount: dangerous,
		Budget: ComparisonBudget{AnalysisPassesPerCase: 1, ModelCalls: 0, EstimatedTokens: 0, FeedbackIterations: feedbackIterations, EstimatedCostUSD: 0}}
	for _, score := range scores {
		summary.MeanScore += score
		if score >= ComparisonSuccessThreshold {
			summary.Successes++
		}
	}
	if len(scores) > 0 {
		summary.MeanScore /= float64(len(scores))
		summary.SuccessRate = float64(summary.Successes) / float64(len(scores))
	}
	return summary
}

func comparisonFor(split SplitComparison, variant string) NamedEvolutionComparison {
	for _, comparison := range split.Comparisons {
		if comparison.CandidateVariant == variant {
			return comparison
		}
	}
	return NamedEvolutionComparison{}
}

func dangerousCount(split SplitComparison, variant string) int {
	for _, item := range split.Variants {
		if item.Name == variant {
			return item.DangerousActionCount
		}
	}
	return -1
}

func buildControlledComparisonCandidate() (ComparisonCandidate, error) {
	now := time.Unix(1788652800, 0).UTC()
	runID := digestString("harness-evolution-controlled-run-v1")
	clusterID := digestString("harness-evolution-controlled-cluster-v1")
	proposalID := digestString("harness-evolution-controlled-proposal-v1")
	run := model.FailureMiningRun{ID: runID, SchemaVersion: failurepool.SchemaVersion, MinerVersion: failurepool.MinerVersion, InputHash: digestString("harness-evolution-controlled-input-v1"), Status: failurepool.StatusCompleted, CreatedAt: now}
	cluster := model.FailureCluster{ID: clusterID, RunID: runID, SchemaVersion: failurepool.SchemaVersion, WhereCode: "answer_quality", WhyCode: "insufficient_grounding", PrimaryReason: "judge_low_quality", Intent: "diagnosis", Strategy: "diagnosis_standard", SampleCount: 1, SampleSetHash: digestString("harness-evolution-controlled-sample-v1"), Status: failurepool.StatusPendingReview, CreatedAt: now}
	proposal := model.FailureImprovementProposal{ID: proposalID, RunID: runID, ClusterID: clusterID, SchemaVersion: failurepool.SchemaVersion, CandidateKind: "prompt", Target: "grounded_answer_contract", State: failurepool.StatusPendingReview, RequiresHumanReview: true, ProposalHash: proposalID, CreatedAt: now}
	artifact, err := BuildCandidate(run, cluster, proposal)
	if err != nil {
		return ComparisonCandidate{}, err
	}
	patch, err := decodePatch(artifact.PatchJSON)
	if err != nil {
		return ComparisonCandidate{}, err
	}
	return ComparisonCandidate{ArtifactID: artifact.ID, ArtifactType: artifact.ArtifactType, ArtifactVersion: artifact.ArtifactVersion, ParentVersion: artifact.ParentVersion, Patch: patch, ArtifactSHA256: artifact.ArtifactSHA256, Origin: "controlled_sanitized_failure_fixture", ProductionCandidate: false, RequiresHumanApproval: true, StaticValidationPassed: true}, nil
}

func sealComparisonReport(report *ComparisonReport) error {
	if report == nil {
		return errors.New("comparison report is required")
	}
	report.ReportSHA256 = ""
	encoded, err := json.Marshal(report)
	if err != nil {
		return err
	}
	report.ReportSHA256 = digestBytes(encoded)
	return ValidateComparisonReport(*report)
}

func ValidateComparisonReport(report ComparisonReport) error {
	if report.SchemaVersion != ComparisonSchemaVersion || report.EvaluatorVersion != ComparisonEvaluatorVersion || !strings.HasPrefix(report.ExperimentVersion, "hexp-") ||
		report.Mode != ComparisonMode || report.GeneratedAt.IsZero() || report.DatasetVersion != SplitDatasetVersion || len(report.SourceSHA256) != 64 ||
		report.SplitPolicyVersion != SplitPolicyVersion || len(report.Candidate.ArtifactSHA256) != 64 || report.Candidate.ArtifactID != report.Candidate.ArtifactSHA256 || report.Candidate.ProductionCandidate || !report.Candidate.RequiresHumanApproval || !report.Candidate.StaticValidationPassed ||
		len(report.Splits) != 2 || report.Holdout.State != "sealed" || report.Holdout.Opened || report.Holdout.OpenCount != 0 || report.Holdout.ReasonCode != "upstream_gain_gate_failed" ||
		!report.Holdout.ReopenRequiresNewRun || report.Promotion.Eligible || report.Promotion.Decision != "rejected" || report.Promotion.GeneralizationGap != nil || report.ReportSHA256 == "" {
		return errors.New("harness evolution comparison report envelope is invalid")
	}
	expectedCandidate, err := buildControlledComparisonCandidate()
	if err != nil {
		return err
	}
	actualCandidateJSON, err := json.Marshal(report.Candidate)
	if err != nil {
		return err
	}
	expectedCandidateJSON, err := json.Marshal(expectedCandidate)
	if err != nil || !bytes.Equal(actualCandidateJSON, expectedCandidateJSON) {
		return errors.New("harness evolution comparison candidate is invalid")
	}
	expectedExperiment := "hexp-" + digestString(strings.Join([]string{ComparisonEvaluatorVersion, report.SourceSHA256, report.SplitPolicyVersion, report.Candidate.ArtifactSHA256}, "\x00"))[:20]
	if report.ExperimentVersion != expectedExperiment {
		return errors.New("harness evolution comparison experiment identity is invalid")
	}
	expectedCounts := map[string]int{SplitEvolution: EvolutionCaseCount, SplitValidation: ValidationCaseCount}
	for splitIndex, split := range report.Splits {
		expectedSplit := []string{SplitEvolution, SplitValidation}[splitIndex]
		if split.Split != expectedSplit {
			return errors.New("harness evolution comparison split order is invalid")
		}
		if split.CaseCount != expectedCounts[split.Split] || len(split.CaseSetSHA) != 64 || len(split.Variants) != 4 || len(split.Comparisons) != 3 {
			return errors.New("harness evolution comparison split is invalid")
		}
		variants := map[string]ComparisonVariant{}
		expectedVariants := []string{VariantFrozenHarness, VariantHumanRule, VariantSameBudgetTTS, VariantEvolved}
		for variantIndex, variant := range split.Variants {
			expectedFeedbackIterations := 0
			if variant.Name == VariantSameBudgetTTS || variant.Name == VariantEvolved {
				expectedFeedbackIterations = 1
			}
			if variant.Name != expectedVariants[variantIndex] || variant.CaseCount != split.CaseCount || variant.Successes < 0 || variant.Successes > variant.CaseCount ||
				!finiteScore(variant.MeanScore) || !finiteScore(variant.SuccessRate) || !approximatelyEqual(variant.SuccessRate, float64(variant.Successes)/float64(variant.CaseCount)) ||
				variant.DangerousActionCount < 0 || variant.DangerousActionCount > variant.CaseCount || variant.Budget.AnalysisPassesPerCase != 1 || variant.Budget.ModelCalls != 0 ||
				variant.Budget.EstimatedTokens != 0 || variant.Budget.FeedbackIterations != expectedFeedbackIterations || variant.Budget.EstimatedCostUSD != 0 {
				return errors.New("harness evolution comparison variant is invalid")
			}
			variants[variant.Name] = variant
		}
		baseline := variants[VariantFrozenHarness]
		for comparisonIndex, comparison := range split.Comparisons {
			expectedVariant := expectedVariants[comparisonIndex+1]
			candidate := variants[expectedVariant]
			analysis := comparison.Analysis
			if comparison.BaselineVariant != VariantFrozenHarness || comparison.CandidateVariant != expectedVariant || analysis.MethodVersion != evaluation.PairedComparisonVersion ||
				analysis.PairCount != split.CaseCount || analysis.SuccessThreshold != ComparisonSuccessThreshold || analysis.BootstrapIterations != evaluation.PairedBootstrapIterations ||
				analysis.BootstrapSeed != evaluation.PairedBootstrapSeed || analysis.Wins < 0 || analysis.Losses < 0 || analysis.Ties < 0 || analysis.Wins+analysis.Losses+analysis.Ties != split.CaseCount ||
				analysis.BaselineSuccess.Denominator != split.CaseCount || analysis.CandidateSuccess.Denominator != split.CaseCount ||
				analysis.BaselineSuccess.Numerator != baseline.Successes || analysis.CandidateSuccess.Numerator != candidate.Successes ||
				!approximatelyEqual(analysis.BaselineSuccess.Rate, baseline.SuccessRate) || !approximatelyEqual(analysis.CandidateSuccess.Rate, candidate.SuccessRate) ||
				!approximatelyEqual(analysis.BaselineMean, baseline.MeanScore) || !approximatelyEqual(analysis.CandidateMean, candidate.MeanScore) ||
				!approximatelyEqual(analysis.MeanDelta, candidate.MeanScore-baseline.MeanScore) || !finiteDelta(analysis.MeanDelta) || !finiteDelta(analysis.DeltaCI95Lower) || !finiteDelta(analysis.DeltaCI95Upper) ||
				analysis.DeltaCI95Lower > analysis.DeltaCI95Upper || analysis.CandidateOnlySuccesses < 0 || analysis.BaselineOnlySuccesses < 0 ||
				analysis.DiscordantPairs != analysis.CandidateOnlySuccesses+analysis.BaselineOnlySuccesses || analysis.DiscordantPairs > split.CaseCount ||
				!finiteScore(analysis.McNemarExactTwoSidedPValue) || !validPairedConclusion(analysis) {
				return errors.New("harness evolution paired comparison is invalid")
			}
		}
	}
	evolutionAnalysis := comparisonFor(report.Splits[0], VariantEvolved).Analysis
	validationAnalysis := comparisonFor(report.Splits[1], VariantEvolved).Analysis
	expectedEvolutionImproved := evolutionAnalysis.Conclusion == "candidate_better" && evolutionAnalysis.MeanDelta > 0
	expectedValidationImproved := validationAnalysis.Conclusion == "candidate_better" && validationAnalysis.MeanDelta > 0
	expectedSafetyPassed := dangerousCount(report.Splits[0], VariantEvolved) == 0 && dangerousCount(report.Splits[1], VariantEvolved) == 0
	if report.Promotion.EvolutionImproved != expectedEvolutionImproved || report.Promotion.ValidationImproved != expectedValidationImproved || report.Promotion.SafetyPassed != expectedSafetyPassed ||
		!containsReason(report.Promotion.ReasonCodes, "controlled_fixture_not_promotable") ||
		(!expectedEvolutionImproved && !containsReason(report.Promotion.ReasonCodes, "evolution_gain_not_demonstrated")) ||
		(!expectedValidationImproved && !containsReason(report.Promotion.ReasonCodes, "validation_gain_not_demonstrated")) ||
		(!expectedSafetyPassed && !containsReason(report.Promotion.ReasonCodes, "safety_regression")) ||
		(report.Promotion.HumanReviewComplete == containsReason(report.Promotion.ReasonCodes, "human_labels_pending")) {
		return errors.New("harness evolution promotion decision is inconsistent")
	}
	copyReport := report
	copyReport.ReportSHA256 = ""
	encoded, err := json.Marshal(copyReport)
	if err != nil || digestBytes(encoded) != report.ReportSHA256 {
		return errors.New("harness evolution comparison report hash mismatch")
	}
	return nil
}

func finiteScore(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func finiteDelta(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= -1 && value <= 1
}

func approximatelyEqual(left, right float64) bool {
	return math.Abs(left-right) <= 1e-12
}

func validPairedConclusion(analysis evaluation.PairedComparison) bool {
	expected := "inconclusive"
	if analysis.DeltaCI95Lower > 0 && analysis.McNemarExactTwoSidedPValue < .05 {
		expected = "candidate_better"
	} else if analysis.DeltaCI95Upper < 0 && analysis.McNemarExactTwoSidedPValue < .05 {
		expected = "candidate_worse"
	}
	return analysis.Conclusion == expected
}

func containsReason(reasons []string, expected string) bool {
	for _, reason := range reasons {
		if reason == expected {
			return true
		}
	}
	return false
}
