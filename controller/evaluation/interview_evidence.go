package evaluation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"GopherAI/common/mysql"
	evaldomain "GopherAI/internal/evaluation"
	"GopherAI/internal/judgecalibration"
	"GopherAI/internal/perfeval"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const (
	interviewEvidenceSchemaVersion = "interview-evidence-package-v1"
	defaultReleaseManifestPath     = "release-manifest.json"
	maxReleaseManifestBytes        = 64 << 10
)

type interviewReleaseManifest struct {
	ReleaseID          string            `json:"release_id"`
	Branch             string            `json:"branch"`
	GitSHA             string            `json:"git_sha"`
	SourceDirty        bool              `json:"source_dirty"`
	BuiltAt            time.Time         `json:"built_at"`
	BuildStrategy      string            `json:"build_strategy"`
	Target             string            `json:"target"`
	GoVersion          string            `json:"go_version"`
	GoBuildFlags       []string          `json:"go_build_flags"`
	IncludedComponents []string          `json:"included_components"`
	ConfigIncluded     bool              `json:"config_included"`
	Migrations         []json.RawMessage `json:"migrations"`
	Rollback           string            `json:"rollback"`
}

type InterviewEvidenceSource struct {
	Name              string    `json:"name"`
	Kind              string    `json:"kind"`
	Version           string    `json:"version"`
	SHA256            string    `json:"sha256"`
	GeneratedAt       time.Time `json:"generated_at"`
	HumanReviewStatus string    `json:"human_review_status"`
}

type InterviewEvidenceInterval struct {
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
}

type InterviewEvidenceMetric struct {
	Name        string                     `json:"name"`
	Value       float64                    `json:"value"`
	Unit        string                     `json:"unit"`
	Numerator   *int                       `json:"numerator,omitempty"`
	Denominator *int                       `json:"denominator,omitempty"`
	CI95        *InterviewEvidenceInterval `json:"ci95,omitempty"`
	PValue      *float64                   `json:"p_value,omitempty"`
}

type InterviewEvidenceStatement struct {
	ID                   string                    `json:"id"`
	Category             string                    `json:"category"`
	Title                string                    `json:"title"`
	Status               string                    `json:"status"`
	ResumeMetricEligible bool                      `json:"resume_metric_eligible"`
	Claim                string                    `json:"claim"`
	Metrics              []InterviewEvidenceMetric `json:"metrics"`
	SourceRefs           []string                  `json:"source_refs"`
	Blockers             []string                  `json:"blockers"`
	ForbiddenOverclaims  []string                  `json:"forbidden_overclaims"`
}

type InterviewEvidencePackage struct {
	SchemaVersion      string                       `json:"schema_version"`
	PackageSHA256      string                       `json:"package_sha256"`
	ReleaseID          string                       `json:"release_id"`
	GitSHA             string                       `json:"git_sha"`
	BuildStrategy      string                       `json:"build_strategy"`
	Target             string                       `json:"target"`
	AllSourcesVerified bool                         `json:"all_sources_verified"`
	ResumeReadyClaims  int                          `json:"resume_ready_claims"`
	TotalClaims        int                          `json:"total_claims"`
	Status             string                       `json:"status"`
	Sources            []InterviewEvidenceSource    `json:"sources"`
	Statements         []InterviewEvidenceStatement `json:"statements"`
	Guardrails         []string                     `json:"guardrails"`
	Limitations        []string                     `json:"limitations"`
}

type interviewEvidenceInputs struct {
	Release          interviewReleaseManifest
	ReleaseSHA       string
	Unified          evaldomain.UnifiedEvaluationReport
	UnifiedSHA       string
	Paired           PairedSummaryResponse
	Performance      perfeval.Report
	JudgeCalibration judgecalibration.Audit
}

type InterviewEvidenceService interface {
	Build(context.Context, string) (InterviewEvidencePackage, error)
}

type fileInterviewEvidenceService struct {
	releasePath   string
	unified       UnifiedReportStore
	collaboration CollaborationReportStore
	parentContext ParentContextReportStore
	performance   PerformanceReportStore
	judge         *judgecalibration.Service
}

func newDefaultInterviewEvidenceService() *fileInterviewEvidenceService {
	return &fileInterviewEvidenceService{
		releasePath:   defaultReleaseManifestPath,
		unified:       NewFileUnifiedReportStore(defaultUnifiedReportPath),
		collaboration: NewFileCollaborationReportStore(defaultCollaborationReportPath),
		parentContext: NewFileParentContextReportStore(defaultParentContextReportPath),
		performance:   filePerformanceReportStore{path: "/root/GopherAI_Runtime/perf/latest.json"},
		judge: judgecalibration.NewService(
			judgecalibration.NewFileArtifactStore(judgecalibration.DefaultDatasetPath, judgecalibration.DefaultReportPath),
			judgecalibration.NewGormRepository(mysql.DB), time.Now,
		),
	}
}

func (service *fileInterviewEvidenceService) Build(ctx context.Context, reviewer string) (InterviewEvidencePackage, error) {
	if service == nil || service.unified == nil || service.collaboration == nil || service.parentContext == nil || service.performance == nil || service.judge == nil {
		return InterviewEvidencePackage{}, errors.New("interview evidence service is unavailable")
	}
	release, releaseSHA, err := loadInterviewReleaseManifest(service.releasePath)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	unified, unifiedSHA, err := service.unified.Load()
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	collaboration, collaborationSHA, err := service.collaboration.Load(ctx)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	parent, parentSHA, err := service.parentContext.Load(ctx)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	paired, err := buildPairedSummary(collaboration, collaborationSHA, parent, parentSHA)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	performance, err := service.performance.Load()
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	judgeAudit, err := service.judge.Audit(ctx, reviewer)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	return buildInterviewEvidencePackage(interviewEvidenceInputs{
		Release: release, ReleaseSHA: releaseSHA, Unified: unified, UnifiedSHA: unifiedSHA,
		Paired: paired, Performance: performance, JudgeCalibration: judgeAudit,
	})
}

func loadInterviewReleaseManifest(path string) (interviewReleaseManifest, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return interviewReleaseManifest{}, "", err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxReleaseManifestBytes+1))
	if err != nil || len(encoded) == 0 || len(encoded) > maxReleaseManifestBytes {
		return interviewReleaseManifest{}, "", errors.New("release manifest is unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var manifest interviewReleaseManifest
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return manifest, "", errors.New("release manifest has trailing content")
	}
	if manifest.ReleaseID == "" || manifest.Branch == "" || len(manifest.GitSHA) != 40 || manifest.BuiltAt.IsZero() || manifest.SourceDirty || manifest.BuildStrategy == "" || manifest.Target == "" || len(manifest.IncludedComponents) < 5 {
		return manifest, "", errors.New("release manifest identity is invalid")
	}
	digest := sha256.Sum256(encoded)
	return manifest, hex.EncodeToString(digest[:]), nil
}

func buildInterviewEvidencePackage(input interviewEvidenceInputs) (InterviewEvidencePackage, error) {
	if len(input.ReleaseSHA) != 64 || len(input.UnifiedSHA) != 64 || input.Release.SourceDirty || input.Release.ReleaseID == "" || input.Release.GitSHA == "" {
		return InterviewEvidencePackage{}, errors.New("interview evidence release input is invalid")
	}
	if err := evaldomain.ValidateUnifiedEvaluationReport(input.Unified); err != nil {
		return InterviewEvidencePackage{}, err
	}
	if input.Paired.SchemaVersion != pairedSummarySchemaVersion || len(input.Paired.AnalysisSHA256) != 64 || len(input.Paired.Comparisons) != 2 || len(input.Paired.Sources) != 2 {
		return InterviewEvidencePackage{}, errors.New("paired evidence input is invalid")
	}
	if err := perfeval.Validate(input.Performance, true); err != nil {
		return InterviewEvidencePackage{}, err
	}
	if input.JudgeCalibration.SchemaVersion != judgecalibration.SchemaVersion || len(input.JudgeCalibration.ReportSHA256) != 64 || input.JudgeCalibration.CaseCount != evaldomain.JudgeCalibrationCaseCount || len(input.JudgeCalibration.Cases) != evaldomain.JudgeCalibrationCaseCount {
		return InterviewEvidencePackage{}, errors.New("judge calibration evidence input is invalid")
	}
	collaboration, ok := pairedComparisonByName(input.Paired, "collaboration_target_quality")
	if !ok {
		return InterviewEvidencePackage{}, errors.New("collaboration paired evidence is missing")
	}
	parent, ok := pairedComparisonByName(input.Paired, "parent_context_target_quality")
	if !ok {
		return InterviewEvidencePackage{}, errors.New("parent-context paired evidence is missing")
	}

	sources := []InterviewEvidenceSource{
		{Name: "current_release_manifest", Kind: "release", Version: input.Release.GitSHA, SHA256: input.ReleaseSHA, GeneratedAt: input.Release.BuiltAt.UTC(), HumanReviewStatus: "not_applicable"},
		{Name: "unified_evaluation", Kind: "evaluation", Version: input.Unified.CandidateVersion, SHA256: input.UnifiedSHA, GeneratedAt: input.Unified.GeneratedAt.UTC(), HumanReviewStatus: reviewStatus(input.Unified.Decision.HumanReviewed)},
		{Name: "paired_analysis", Kind: "statistical_analysis", Version: input.Paired.MethodVersion, SHA256: input.Paired.AnalysisSHA256, GeneratedAt: input.Paired.GeneratedAt.UTC(), HumanReviewStatus: reviewStatus(input.Paired.HumanReviewed)},
		{Name: "performance_evaluation", Kind: "ecs_performance", Version: input.Performance.Release.ID, SHA256: input.Performance.ReportSHA256, GeneratedAt: input.Performance.GeneratedAt.UTC(), HumanReviewStatus: "not_applicable"},
		{Name: "judge_calibration", Kind: "judge_calibration", Version: input.JudgeCalibration.JudgePrompt, SHA256: input.JudgeCalibration.ReportSHA256, GeneratedAt: input.JudgeCalibration.JudgeGeneratedAt.UTC(), HumanReviewStatus: fmt.Sprintf("current_reviewer_%d_of_%d", input.JudgeCalibration.Agreement.ReviewedCases, input.JudgeCalibration.Agreement.RequiredCases)},
	}
	for _, source := range input.Paired.Sources {
		sources = append(sources, InterviewEvidenceSource{Name: source.Name + "_ab", Kind: "paired_source", Version: source.CandidateVersion, SHA256: source.ReportSHA256, GeneratedAt: source.GeneratedAt.UTC(), HumanReviewStatus: reviewStatus(source.HumanReviewed)})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	for _, source := range sources {
		if source.Name == "" || source.Version == "" || len(source.SHA256) != 64 || source.GeneratedAt.IsZero() || source.HumanReviewStatus == "" {
			return InterviewEvidencePackage{}, errors.New("interview evidence source identity is invalid")
		}
	}

	statements := []InterviewEvidenceStatement{
		buildUnifiedEvidenceStatement(input.Unified),
		buildCollaborationEvidenceStatement(collaboration),
		buildParentEvidenceStatement(parent),
		buildPerformanceEvidenceStatement(input.Performance),
		buildJudgeEvidenceStatement(input.JudgeCalibration),
	}
	resumeReady := 0
	for _, statement := range statements {
		if statement.ResumeMetricEligible {
			resumeReady++
		}
	}
	status := "evidence_ready_with_human_blockers"
	if resumeReady == len(statements) {
		status = "all_claims_resume_ready"
	}
	result := InterviewEvidencePackage{
		SchemaVersion: interviewEvidenceSchemaVersion, ReleaseID: input.Release.ReleaseID, GitSHA: input.Release.GitSHA,
		BuildStrategy: input.Release.BuildStrategy, Target: input.Release.Target, AllSourcesVerified: true,
		ResumeReadyClaims: resumeReady, TotalClaims: len(statements), Status: status, Sources: sources, Statements: statements,
		Guardrails: []string{"source_hash_required", "numerator_denominator_required_for_rates", "negative_results_preserved", "pending_human_review_blocks_resume_metric", "no_active_policy_write"},
		Limitations: []string{
			"证据包聚合已存在的不可变报告，不会把多个不同任务、模型调用或成本口径合并成一个总体收益率。",
			"ResumeMetricEligible 只表示该条数字具备当前证据链；表述时仍必须同时披露样本量、环境和限制。",
			"当前多 Agent、父子 RAG、Full 320 与 Judge 人工一致性仍受人工复核阻塞，不能写成已上线收益。",
		},
	}
	if err := finalizeInterviewEvidencePackage(&result); err != nil {
		return InterviewEvidencePackage{}, err
	}
	return result, nil
}

func reviewStatus(reviewed bool) string {
	if reviewed {
		return "complete"
	}
	return "pending_user"
}

func pairedComparisonByName(report PairedSummaryResponse, name string) (NamedPairedComparison, bool) {
	for _, item := range report.Comparisons {
		if item.Name == name {
			return item, true
		}
	}
	return NamedPairedComparison{}, false
}

func buildUnifiedEvidenceStatement(report evaldomain.UnifiedEvaluationReport) InterviewEvidenceStatement {
	eligible := report.Decision.BaselineEligible && report.Decision.HumanReviewed
	status, blockers := "resume_ready", []string{}
	if !eligible {
		status, blockers = "candidate_only", append([]string{}, report.Decision.Blockers...)
	}
	return InterviewEvidenceStatement{
		ID: "full_320_evaluation", Category: "evaluation", Title: "Full 320 统一评测目录与执行覆盖", Status: status, ResumeMetricEligible: eligible,
		Claim: fmt.Sprintf("目录校验 %d/%d；可执行集完成 %d/%d；当前只按报告门禁解释。", report.Coverage.CatalogValidatedCases, report.Coverage.CatalogCases, report.Coverage.CompletedCases, report.Coverage.ExecutableCases),
		Metrics: []InterviewEvidenceMetric{
			rateMetric("catalog_validation_rate", report.Coverage.CatalogValidatedCases, report.Coverage.CatalogCases),
			rateMetric("execution_coverage", report.Coverage.ExecutableCases, report.Coverage.CatalogCases),
			rateMetric("completion_rate", report.Coverage.CompletedCases, report.Coverage.ExecutableCases),
		},
		SourceRefs: []string{"unified_evaluation"}, Blockers: blockers,
		ForbiddenOverclaims: []string{"不得把 pending_user 目录称为人工审核基线", "不得把 300 条执行覆盖说成 320 条全部调用模型完成"},
	}
}

func buildCollaborationEvidenceStatement(item NamedPairedComparison) InterviewEvidenceStatement {
	a := item.Analysis
	p := a.McNemarExactTwoSidedPValue
	return InterviewEvidenceStatement{
		ID: "multi_agent_paired_gain", Category: "multi_agent", Title: "复杂诊断多 Agent 成对收益", Status: "candidate_only", ResumeMetricEligible: false,
		Claim: fmt.Sprintf("同一复杂诊断样本上，成功数从 %d/%d 到 %d/%d，平均质量差 %.1f%%。", a.BaselineSuccess.Numerator, a.BaselineSuccess.Denominator, a.CandidateSuccess.Numerator, a.CandidateSuccess.Denominator, a.MeanDelta*100),
		Metrics: []InterviewEvidenceMetric{
			rateMetric("baseline_success_rate", a.BaselineSuccess.Numerator, a.BaselineSuccess.Denominator),
			rateMetric("candidate_success_rate", a.CandidateSuccess.Numerator, a.CandidateSuccess.Denominator),
			{Name: "paired_mean_quality_delta", Value: a.MeanDelta, Unit: "ratio", CI95: &InterviewEvidenceInterval{Lower: a.DeltaCI95Lower, Upper: a.DeltaCI95Upper}, PValue: &p},
		},
		SourceRefs: []string{"collaboration_ab", "paired_analysis"}, Blockers: []string{"human_review_pending", "promotion_gate_closed"},
		ForbiddenOverclaims: []string{"不得称为线上解决率提升", "不得省略 10 条目标样本与 pending_user 标签", "不得声称已自动切流"},
	}
}

func buildParentEvidenceStatement(item NamedPairedComparison) InterviewEvidenceStatement {
	a := item.Analysis
	p := a.McNemarExactTwoSidedPValue
	return InterviewEvidenceStatement{
		ID: "parent_context_negative_result", Category: "rag", Title: "父子上下文 RAG 负结果", Status: "negative_result", ResumeMetricEligible: false,
		Claim: fmt.Sprintf("目标样本成功数 %d/%d 到 %d/%d，成对质量差 %.1f%%，未证明净收益。", a.BaselineSuccess.Numerator, a.BaselineSuccess.Denominator, a.CandidateSuccess.Numerator, a.CandidateSuccess.Denominator, a.MeanDelta*100),
		Metrics: []InterviewEvidenceMetric{
			rateMetric("baseline_success_rate", a.BaselineSuccess.Numerator, a.BaselineSuccess.Denominator),
			rateMetric("candidate_success_rate", a.CandidateSuccess.Numerator, a.CandidateSuccess.Denominator),
			{Name: "paired_mean_quality_delta", Value: a.MeanDelta, Unit: "ratio", CI95: &InterviewEvidenceInterval{Lower: a.DeltaCI95Lower, Upper: a.DeltaCI95Upper}, PValue: &p},
		},
		SourceRefs: []string{"parent_context_ab", "paired_analysis"}, Blockers: []string{"no_measured_net_gain", "human_review_pending"},
		ForbiddenOverclaims: []string{"不得把实现了父子检索写成质量提升", "不得隐藏额外 Token 成本", "不得启用默认权重"},
	}
}

func buildPerformanceEvidenceStatement(report perfeval.Report) InterviewEvidenceStatement {
	status := "technical_gate_failed"
	if report.Gates.TechnicalPassed {
		status = "resume_ready"
	}
	return InterviewEvidenceStatement{
		ID: "bounded_ecs_performance", Category: "performance", Title: "2 vCPU 小内存 ECS 受限性能纵切", Status: status, ResumeMetricEligible: report.Gates.TechnicalPassed,
		Claim: fmt.Sprintf("热路径 %d/%d 成功，并发 %d，端到端 P95 %.0fms，TTFT P95 %.0fms。", report.Hot.Successes, report.Hot.Requests, report.Hot.Concurrency, report.Hot.TotalLatency.P95MS, report.Hot.TTFT.P95MS),
		Metrics: []InterviewEvidenceMetric{
			rateMetric("hot_success_rate", report.Hot.Successes, report.Hot.Requests),
			{Name: "hot_total_latency_p95", Value: report.Hot.TotalLatency.P95MS, Unit: "ms"},
			{Name: "hot_ttft_p95", Value: report.Hot.TTFT.P95MS, Unit: "ms"},
			{Name: "estimated_tokens_per_100", Value: report.Hot.TokensPer100, Unit: "estimated_tokens"},
		},
		SourceRefs: []string{"performance_evaluation"}, Blockers: append([]string{}, report.Gates.Failures...),
		ForbiddenOverclaims: []string{"不得外推到 RAG、多 Agent 或工具路径", "不得把估算 Token 称为供应商账单", "不得把单 Release 首请求称为整机全冷启动"},
	}
}

func buildJudgeEvidenceStatement(audit judgecalibration.Audit) InterviewEvidenceStatement {
	completed := 0
	for _, item := range audit.Cases {
		if item.Judge.Status == evaldomain.JudgeStatusComplete {
			completed++
		}
	}
	eligible := audit.JudgeTechnical && audit.Agreement.CalibrationGatePassed && audit.Agreement.AutomationUsePermitted
	status, blockers := "resume_ready", []string{}
	if !eligible {
		status = "calibration_pending"
		if !audit.JudgeTechnical {
			blockers = append(blockers, "judge_technical_gate_failed")
		}
		if audit.Agreement.ReviewedCases < audit.Agreement.RequiredCases {
			blockers = append(blockers, "human_calibration_incomplete")
		} else if !audit.Agreement.CalibrationGatePassed {
			blockers = append(blockers, "kappa_gate_failed")
		}
	}
	return InterviewEvidenceStatement{
		ID: "judge_human_calibration", Category: "evaluation", Title: "LLM-as-a-Judge 人工一致性", Status: status, ResumeMetricEligible: eligible,
		Claim: fmt.Sprintf("Judge 技术完成 %d/%d；当前人工复核 %d/%d；κ 门禁 %.2f。", completed, audit.CaseCount, audit.Agreement.ReviewedCases, audit.Agreement.RequiredCases, audit.Agreement.KappaGate),
		Metrics: []InterviewEvidenceMetric{
			rateMetric("judge_technical_completion", completed, audit.CaseCount),
			rateMetric("human_review_completion", audit.Agreement.ReviewedCases, audit.Agreement.RequiredCases),
			{Name: "linear_weighted_kappa", Value: audit.Agreement.LinearWeightedKappa, Unit: "coefficient"},
		},
		SourceRefs: []string{"judge_calibration"}, Blockers: blockers,
		ForbiddenOverclaims: []string{"不得把 30/30 技术调用称为人工校准通过", "不得隐瞒 Judge 与回答模型可能同家族", "单一复核人不得称为双人独立标注"},
	}
}

func rateMetric(name string, numerator, denominator int) InterviewEvidenceMetric {
	value := 0.0
	if denominator > 0 {
		value = float64(numerator) / float64(denominator)
	}
	n, d := numerator, denominator
	return InterviewEvidenceMetric{Name: name, Value: value, Unit: "ratio", Numerator: &n, Denominator: &d}
}

func finalizeInterviewEvidencePackage(report *InterviewEvidencePackage) error {
	if report == nil || report.SchemaVersion != interviewEvidenceSchemaVersion || report.ReleaseID == "" || len(report.GitSHA) != 40 || !report.AllSourcesVerified || report.TotalClaims != len(report.Statements) || report.ResumeReadyClaims < 0 || report.ResumeReadyClaims > report.TotalClaims || len(report.Sources) < 7 || len(report.Statements) != 5 {
		return errors.New("interview evidence package is invalid")
	}
	sourceNames := make(map[string]struct{}, len(report.Sources))
	for _, source := range report.Sources {
		if source.Name == "" || source.Kind == "" || source.Version == "" || source.GeneratedAt.IsZero() || source.HumanReviewStatus == "" || len(source.SHA256) != 64 {
			return errors.New("interview evidence source is invalid")
		}
		if _, err := hex.DecodeString(source.SHA256); err != nil {
			return errors.New("interview evidence source hash is invalid")
		}
		if _, duplicate := sourceNames[source.Name]; duplicate {
			return errors.New("interview evidence source names must be unique")
		}
		sourceNames[source.Name] = struct{}{}
	}
	ready, statementIDs := 0, map[string]struct{}{}
	for _, statement := range report.Statements {
		if statement.ID == "" || statement.Category == "" || statement.Title == "" || statement.Status == "" || statement.Claim == "" || len(statement.Metrics) == 0 || len(statement.SourceRefs) == 0 || len(statement.ForbiddenOverclaims) == 0 {
			return errors.New("interview evidence statement is incomplete")
		}
		if _, duplicate := statementIDs[statement.ID]; duplicate {
			return errors.New("interview evidence statement ids must be unique")
		}
		statementIDs[statement.ID] = struct{}{}
		if statement.ResumeMetricEligible {
			ready++
		}
		for _, sourceRef := range statement.SourceRefs {
			if _, exists := sourceNames[sourceRef]; !exists {
				return errors.New("interview evidence statement references an unknown source")
			}
		}
		for _, metric := range statement.Metrics {
			if metric.Name == "" || metric.Unit == "" || math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) {
				return errors.New("interview evidence metric is invalid")
			}
			if (metric.Numerator == nil) != (metric.Denominator == nil) {
				return errors.New("interview evidence rate accounting is incomplete")
			}
			if metric.Numerator != nil && (*metric.Denominator <= 0 || *metric.Numerator < 0 || *metric.Numerator > *metric.Denominator || math.Abs(metric.Value-float64(*metric.Numerator)/float64(*metric.Denominator)) > 1e-9) {
				return errors.New("interview evidence rate accounting is inconsistent")
			}
			if metric.CI95 != nil && (math.IsNaN(metric.CI95.Lower) || math.IsNaN(metric.CI95.Upper) || metric.CI95.Lower > metric.CI95.Upper) {
				return errors.New("interview evidence interval is invalid")
			}
			if metric.PValue != nil && (math.IsNaN(*metric.PValue) || *metric.PValue < 0 || *metric.PValue > 1) {
				return errors.New("interview evidence p-value is invalid")
			}
		}
	}
	if ready != report.ResumeReadyClaims {
		return errors.New("interview evidence ready count is inconsistent")
	}
	report.PackageSHA256 = ""
	encoded, err := json.Marshal(report)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	report.PackageSHA256 = hex.EncodeToString(digest[:])
	return nil
}

func writeInterviewEvidenceMarkdown(writer io.Writer, report InterviewEvidencePackage) error {
	if writer == nil || len(report.PackageSHA256) != 64 {
		return errors.New("valid interview evidence package is required")
	}
	if _, err := fmt.Fprintf(writer, "# GopherAI 可复现面试证据包\n\n- Package SHA-256：`%s`\n- Release：`%s`\n- Git SHA：`%s`\n- 简历指标就绪：`%d/%d`\n- 状态：`%s`\n\n", report.PackageSHA256, report.ReleaseID, report.GitSHA, report.ResumeReadyClaims, report.TotalClaims, report.Status); err != nil {
		return err
	}
	for _, statement := range report.Statements {
		if _, err := fmt.Fprintf(writer, "## %s\n\n- 状态：`%s`\n- 可作为简历指标：`%t`\n- 可陈述：%s\n- 来源：`%s`\n", statement.Title, statement.Status, statement.ResumeMetricEligible, statement.Claim, strings.Join(statement.SourceRefs, "`, `")); err != nil {
			return err
		}
		if len(statement.Blockers) > 0 {
			if _, err := fmt.Fprintf(writer, "- 阻塞：`%s`\n", strings.Join(statement.Blockers, "`, `")); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(writer, "- 禁止夸大：%s\n\n", strings.Join(statement.ForbiddenOverclaims, "；")); err != nil {
			return err
		}
	}
	return nil
}

type InterviewEvidenceHandler struct{ service InterviewEvidenceService }

func NewInterviewEvidenceHandler(service InterviewEvidenceService) *InterviewEvidenceHandler {
	return &InterviewEvidenceHandler{service: service}
}

func NewDefaultInterviewEvidenceHandler() *InterviewEvidenceHandler {
	return NewInterviewEvidenceHandler(newDefaultInterviewEvidenceService())
}

func (handler *InterviewEvidenceHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeInterviewEvidenceError(ginContext, http.StatusServiceUnavailable, "INTERVIEW_EVIDENCE_UNAVAILABLE", "面试证据包暂不可用")
		return
	}
	format := strings.TrimSpace(ginContext.Query("format"))
	if format != "" && format != "json" && format != "markdown" {
		writeInterviewEvidenceError(ginContext, http.StatusBadRequest, "INVALID_EVIDENCE_FORMAT", "仅支持 json 或 markdown 格式")
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 5*time.Second)
	defer cancel()
	report, err := handler.service.Build(ctx, ginContext.GetString("userName"))
	if err != nil {
		writeInterviewEvidenceError(ginContext, http.StatusServiceUnavailable, "INTERVIEW_EVIDENCE_NOT_READY", "面试证据来源尚未全部就绪")
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.Header("X-Content-Type-Options", "nosniff")
	if format == "markdown" {
		var buffer bytes.Buffer
		if err := writeInterviewEvidenceMarkdown(&buffer, report); err != nil {
			writeInterviewEvidenceError(ginContext, http.StatusServiceUnavailable, "INTERVIEW_EVIDENCE_EXPORT_FAILED", "面试证据包导出失败")
			return
		}
		ginContext.Header("Content-Disposition", `attachment; filename="gopherai-interview-evidence.md"`)
		ginContext.Data(http.StatusOK, "text/markdown; charset=utf-8", buffer.Bytes())
		return
	}
	if format == "json" {
		ginContext.Header("Content-Disposition", `attachment; filename="gopherai-interview-evidence.json"`)
	}
	ginContext.JSON(http.StatusOK, report)
}

func writeInterviewEvidenceError(ginContext *gin.Context, status int, code, message string) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(status, ErrorResponse{SchemaVersion: interviewEvidenceSchemaVersion, Code: code, Message: message, Retryable: status >= 500, TraceID: traceID})
}
