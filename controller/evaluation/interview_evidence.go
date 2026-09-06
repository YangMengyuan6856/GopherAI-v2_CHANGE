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
	"GopherAI/internal/controlrecommendation"
	evaldomain "GopherAI/internal/evaluation"
	"GopherAI/internal/evolution"
	"GopherAI/internal/faultcampaign"
	"GopherAI/internal/judgecalibration"
	"GopherAI/internal/metriccatalog"
	"GopherAI/internal/observability"
	"GopherAI/internal/perfeval"
	"GopherAI/internal/reliabilityeval"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const (
	interviewEvidenceSchemaVersion = "interview-evidence-package-v3"
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
	Release               interviewReleaseManifest
	ReleaseSHA            string
	Unified               evaldomain.UnifiedEvaluationReport
	UnifiedSHA            string
	Paired                PairedSummaryResponse
	Performance           perfeval.Report
	JudgeCalibration      judgecalibration.Audit
	FaultCampaign         faultcampaign.CampaignReport
	FaultGeneratedAt      time.Time
	Reliability           reliabilityeval.Report
	MetricCatalog         metriccatalog.CatalogReport
	Grafana               observability.GrafanaRuntimeSnapshot
	Control               controlrecommendation.AuditSnapshot
	ControlSHA            string
	EvolutionLineage      evolution.Audit
	EvolutionLineageSHA   string
	EvolutionSplit        evolution.SplitAudit
	EvolutionSplitSHA     string
	EvolutionComparison   evolution.ComparisonReport
	EvolutionControl      evolution.ControlAcceptanceReport
	EvolutionPromotion    evolution.PromotionAudit
	EvolutionPromotionSHA string
	EvolutionShadow       evolution.ShadowControlAudit
	EvolutionShadowSHA    string
}

type InterviewEvidenceService interface {
	Build(context.Context, string) (InterviewEvidencePackage, error)
}

type fileInterviewEvidenceService struct {
	releasePath         string
	unified             UnifiedReportStore
	collaboration       CollaborationReportStore
	parentContext       ParentContextReportStore
	performance         PerformanceReportStore
	judge               *judgecalibration.Service
	faultCampaign       FaultCampaignService
	reliability         reliabilityeval.ReportStore
	metricCatalog       metriccatalog.CatalogReport
	metricErr           error
	grafana             GrafanaRuntimeReader
	control             RecommendationController
	evolution           EvolutionService
	evolutionComparison evolution.ComparisonStore
	evolutionControl    evolution.ControlAcceptanceStore
	evolutionPromotion  *evolution.PromotionService
	evolutionShadow     *evolution.ShadowControlService
}

func newDefaultInterviewEvidenceService() *fileInterviewEvidenceService {
	faultService, _ := faultcampaign.NewDefaultService()
	recommendation, _ := controlrecommendation.NewDefaultController()
	metricHandler := NewDefaultMetricCatalogHandler()
	evolutionService, _ := evolution.NewDefaultService()
	promotionService, _ := evolution.NewPromotionService(evolution.NewFileComparisonStore(defaultEvolutionComparisonPath), evolution.NewGormPromotionRepository(mysql.DB), evolution.NewGormPromotionAuthorizer(mysql.DB), time.Now)
	shadowService, _ := evolution.NewShadowControlService(evolution.NewFileComparisonStore(defaultEvolutionComparisonPath), evolution.NewGormShadowControlRepository(mysql.DB), evolution.NewGormPromotionAuthorizer(mysql.DB), evolution.DeterministicShadowEvaluator{}, time.Now)
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
		faultCampaign:       faultService,
		reliability:         reliabilityeval.NewFileStore(defaultReliabilityReportPath),
		metricCatalog:       metricHandler.report,
		metricErr:           metricHandler.err,
		grafana:             observability.NewDefaultGrafanaRuntimeClient(),
		control:             recommendation,
		evolution:           evolutionService,
		evolutionComparison: evolution.NewFileComparisonStore(defaultEvolutionComparisonPath),
		evolutionControl:    evolution.NewFileControlAcceptanceStore(defaultEvolutionControlAcceptancePath),
		evolutionPromotion:  promotionService,
		evolutionShadow:     shadowService,
	}
}

func (service *fileInterviewEvidenceService) Build(ctx context.Context, reviewer string) (InterviewEvidencePackage, error) {
	if service == nil || service.unified == nil || service.collaboration == nil || service.parentContext == nil || service.performance == nil || service.judge == nil ||
		service.faultCampaign == nil || service.reliability == nil || service.metricErr != nil || service.grafana == nil || service.control == nil || service.evolution == nil || service.evolutionComparison == nil || service.evolutionControl == nil || service.evolutionPromotion == nil || service.evolutionShadow == nil {
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
	faultAudit, err := service.faultCampaign.Audit(ctx)
	if err != nil || faultAudit.Latest == nil || faultAudit.LatestCreatedAt.IsZero() {
		return InterviewEvidencePackage{}, errors.New("fault campaign evidence is unavailable")
	}
	reliability, err := service.reliability.Load()
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	grafana, err := service.grafana.Snapshot(ctx)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	control, err := service.control.Audit(ctx)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	controlSHA, err := digestInterviewEvidenceValue(control)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	evolutionLineage, err := service.evolution.Audit(ctx)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	evolutionLineageSHA, err := digestInterviewEvidenceValue(evolutionLineage)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	evolutionSplit, err := evolution.LoadSplitAudit(defaultEvolutionDatasetPath, defaultEvolutionCatalogPath)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	evolutionSplitSHA, err := digestInterviewEvidenceValue(evolutionSplit)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	evolutionComparison, err := service.evolutionComparison.Load()
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	evolutionControl, err := service.evolutionControl.Load()
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	evolutionPromotion, err := service.evolutionPromotion.Audit(ctx, reviewer)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	evolutionPromotionSHA, err := digestInterviewEvidenceValue(evolutionPromotion)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	evolutionShadow, err := service.evolutionShadow.Audit(ctx, reviewer)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	evolutionShadowSHA, err := digestInterviewEvidenceValue(evolutionShadow)
	if err != nil {
		return InterviewEvidencePackage{}, err
	}
	return buildInterviewEvidencePackage(interviewEvidenceInputs{
		Release: release, ReleaseSHA: releaseSHA, Unified: unified, UnifiedSHA: unifiedSHA,
		Paired: paired, Performance: performance, JudgeCalibration: judgeAudit,
		FaultCampaign: *faultAudit.Latest, FaultGeneratedAt: faultAudit.LatestCreatedAt,
		Reliability: reliability, MetricCatalog: service.metricCatalog, Grafana: grafana,
		Control: control, ControlSHA: controlSHA,
		EvolutionLineage: evolutionLineage, EvolutionLineageSHA: evolutionLineageSHA, EvolutionSplit: evolutionSplit, EvolutionSplitSHA: evolutionSplitSHA,
		EvolutionComparison: evolutionComparison, EvolutionControl: evolutionControl, EvolutionPromotion: evolutionPromotion, EvolutionPromotionSHA: evolutionPromotionSHA,
		EvolutionShadow: evolutionShadow, EvolutionShadowSHA: evolutionShadowSHA,
	})
}

func digestInterviewEvidenceValue(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
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
	if err := faultcampaign.ValidateReport(input.FaultCampaign); err != nil || input.FaultGeneratedAt.IsZero() {
		return InterviewEvidencePackage{}, errors.New("fault campaign evidence input is invalid")
	}
	if err := reliabilityeval.ValidateReport(input.Reliability); err != nil {
		return InterviewEvidencePackage{}, err
	}
	if input.MetricCatalog.SchemaVersion != metriccatalog.SchemaVersion || !input.MetricCatalog.Passed || len(input.MetricCatalog.CatalogSHA256) != 64 ||
		input.MetricCatalog.RequiredFamilyCount != input.MetricCatalog.RequiredPresentCount || input.MetricCatalog.ForbiddenLabelHits != 0 || input.MetricCatalog.DuplicateMetricNames != 0 || input.MetricCatalog.ContractMismatchCount != 0 {
		return InterviewEvidencePackage{}, errors.New("metric catalog evidence input is invalid")
	}
	if input.Grafana.SchemaVersion != observability.GrafanaRuntimeSchemaVersion || input.Grafana.Status != "ready" || input.Grafana.CollectedAt.IsZero() ||
		input.Grafana.PublicExposure || !input.Grafana.Dashboard.Passed || len(input.Grafana.Dashboard.DashboardSHA) != 64 {
		return InterviewEvidencePackage{}, errors.New("grafana evidence input is invalid")
	}
	if input.Control.SchemaVersion != controlrecommendation.SchemaVersion || input.Control.Mode != controlrecommendation.ModeRecommendOnly ||
		input.Control.ActivePolicy.Version == "" || len(input.Control.ActivePolicy.SHA256) != 64 || len(input.ControlSHA) != 64 {
		return InterviewEvidencePackage{}, errors.New("control evidence input is invalid")
	}
	if input.EvolutionLineage.SchemaVersion != evolution.SchemaVersion || input.EvolutionLineage.Mode != evolution.Mode || input.EvolutionLineage.ActivePointers != 0 || input.EvolutionLineage.AppliedCount != 0 || len(input.EvolutionLineageSHA) != 64 {
		return InterviewEvidencePackage{}, errors.New("harness evolution lineage evidence is invalid")
	}
	if input.EvolutionSplit.SchemaVersion != evolution.SplitSchemaVersion || input.EvolutionSplit.PolicyVersion != evolution.SplitPolicyVersion || !input.EvolutionSplit.SourceHashVerified || input.EvolutionSplit.TotalCases != evolution.SplitTotalCases || input.EvolutionSplit.CoveredCases != evolution.SplitTotalCases || input.EvolutionSplit.OverlapCount != 0 || input.EvolutionSplit.DuplicateIDCount != 0 || input.EvolutionSplit.HoldoutOpenCount != 0 || len(input.EvolutionSplitSHA) != 64 {
		return InterviewEvidencePackage{}, errors.New("harness evolution split evidence is invalid")
	}
	if err := evolution.ValidateComparisonReport(input.EvolutionComparison); err != nil {
		return InterviewEvidencePackage{}, err
	}
	if err := evolution.ValidateControlAcceptanceReport(input.EvolutionControl); err != nil {
		return InterviewEvidencePackage{}, err
	}
	if input.EvolutionPromotion.SchemaVersion != evolution.PromotionSchemaVersion || input.EvolutionPromotion.Mode != evolution.PromotionMode || input.EvolutionPromotion.ActivePointers != 0 || input.EvolutionPromotion.AttemptCount < input.EvolutionPromotion.RecordedCount+input.EvolutionPromotion.BlockedCount || len(input.EvolutionPromotionSHA) != 64 {
		return InterviewEvidencePackage{}, errors.New("harness promotion evidence is invalid")
	}
	if input.EvolutionShadow.SchemaVersion != evolution.ShadowControlSchemaVersion || input.EvolutionShadow.Mode != evolution.ShadowControlMode || input.EvolutionShadow.Scope != evolution.ShadowPointerScope || input.EvolutionShadow.AffectsLiveTraffic || input.EvolutionShadow.EventCount < input.EvolutionShadow.AppliedCount+input.EvolutionShadow.BlockedCount || len(input.EvolutionShadowSHA) != 64 {
		return InterviewEvidencePackage{}, errors.New("harness shadow control evidence is invalid")
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
		{Name: "fault_campaign", Kind: "fault_injection", Version: input.FaultCampaign.FixtureVersion, SHA256: input.FaultCampaign.ReportSHA256, GeneratedAt: input.FaultGeneratedAt.UTC(), HumanReviewStatus: "not_applicable"},
		{Name: "agent_reliability", Kind: "reliability", Version: input.Reliability.SchemaVersion, SHA256: input.Reliability.ReportSHA256, GeneratedAt: input.Reliability.GeneratedAt.UTC(), HumanReviewStatus: "not_applicable"},
		{Name: "metric_catalog", Kind: "observability_contract", Version: input.MetricCatalog.CatalogVersion, SHA256: input.MetricCatalog.CatalogSHA256, GeneratedAt: input.Release.BuiltAt.UTC(), HumanReviewStatus: "not_applicable"},
		{Name: "grafana_runtime", Kind: "observability_runtime", Version: input.Grafana.GrafanaVersion, SHA256: input.Grafana.Dashboard.DashboardSHA, GeneratedAt: input.Release.BuiltAt.UTC(), HumanReviewStatus: "not_applicable"},
		{Name: "recommend_only_controller", Kind: "control_guardrail", Version: input.Control.ActivePolicy.Version, SHA256: input.ControlSHA, GeneratedAt: controlEvidenceGeneratedAt(input.Control, input.Release.BuiltAt), HumanReviewStatus: "not_applicable"},
		{Name: "harness_evolution_lineage", Kind: "harness_lineage", Version: input.EvolutionLineage.SchemaVersion, SHA256: input.EvolutionLineageSHA, GeneratedAt: input.Release.BuiltAt.UTC(), HumanReviewStatus: "governed"},
		{Name: "harness_evolution_split", Kind: "dataset_split", Version: input.EvolutionSplit.PolicyVersion, SHA256: input.EvolutionSplitSHA, GeneratedAt: input.Release.BuiltAt.UTC(), HumanReviewStatus: "pending_user"},
		{Name: "harness_evolution_comparison", Kind: "paired_evaluation", Version: input.EvolutionComparison.ExperimentVersion, SHA256: input.EvolutionComparison.ReportSHA256, GeneratedAt: input.EvolutionComparison.GeneratedAt.UTC(), HumanReviewStatus: reviewStatus(input.EvolutionComparison.Promotion.HumanReviewComplete)},
		{Name: "harness_control_acceptance", Kind: "control_acceptance", Version: input.EvolutionControl.TransitionVersion, SHA256: input.EvolutionControl.ReportSHA256, GeneratedAt: input.EvolutionControl.GeneratedAt.UTC(), HumanReviewStatus: "not_applicable"},
		{Name: "harness_promotion_audit", Kind: "human_gate", Version: input.EvolutionPromotion.SchemaVersion, SHA256: input.EvolutionPromotionSHA, GeneratedAt: harnessPromotionGeneratedAt(input.EvolutionPromotion, input.Release.BuiltAt), HumanReviewStatus: fmt.Sprintf("attempts_%d", input.EvolutionPromotion.AttemptCount)},
		{Name: "harness_shadow_control", Kind: "isolated_control", Version: input.EvolutionShadow.Mode, SHA256: input.EvolutionShadowSHA, GeneratedAt: harnessShadowGeneratedAt(input.EvolutionShadow, input.Release.BuiltAt), HumanReviewStatus: "governed"},
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
		buildFaultCampaignEvidenceStatement(input.FaultCampaign),
		buildReliabilityEvidenceStatement(input.Reliability),
		buildMetricCatalogEvidenceStatement(input.MetricCatalog),
		buildGrafanaEvidenceStatement(input.Grafana),
		buildControlEvidenceStatement(input.Control),
		buildHarnessEvolutionEvidenceStatement(input),
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
		Guardrails: []string{"source_hash_required", "numerator_denominator_required_for_rates", "negative_results_preserved", "pending_human_review_blocks_resume_metric", "simulation_scope_disclosed", "no_active_policy_write", "harness_candidate_rejection_preserved", "isolated_shadow_not_live_traffic"},
		Limitations: []string{
			"证据包聚合已存在的不可变报告，不会把多个不同任务、模型调用或成本口径合并成一个总体收益率。",
			"ResumeMetricEligible 只表示该条数字具备当前证据链；表述时仍必须同时披露样本量、环境和限制。",
			"当前多 Agent、父子 RAG、Full 320 与 Judge 人工一致性仍受人工复核阻塞，不能写成已上线收益。",
			"故障、恢复与控制器数字来自隔离验收或只建议审计，只能按其边界陈述，不能称为真实线上事故或自动优化收益。",
			"Harness Evolution 当前证据证明了可复现的候选拒绝与控制治理，不证明自动候选提升质量，也不证明 Sealed Holdout 已被打开。",
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

func buildFaultCampaignEvidenceStatement(report faultcampaign.CampaignReport) InterviewEvidenceStatement {
	ready := faultcampaign.ValidateReport(report) == nil
	status, blockers := "resume_ready", []string{}
	if !ready {
		status, blockers = "technical_gate_failed", []string{"fault_campaign_contract_failed"}
	}
	return InterviewEvidenceStatement{
		ID: "observe_only_fault_campaign", Category: "closed_loop", Title: "三类 Observe-only 故障检测与恢复", Status: status, ResumeMetricEligible: ready,
		Claim: fmt.Sprintf("隔离演练检测 %d/%d、恢复 %d/%d、健康对照误报 %d/%d，平均 MTTD %.0fs；生成建议 %d 条、实际应用 %d 条。", report.Summary.DetectedCount, report.Summary.ScenarioCount, report.Summary.RecoveredCount, report.Summary.ScenarioCount, report.Summary.FalsePositives, report.Summary.FalsePositiveChecks, report.Summary.MeanMTTDSeconds, report.Summary.RecommendationCount, report.Summary.AppliedCount),
		Metrics: []InterviewEvidenceMetric{
			rateMetric("fault_detection_rate", report.Summary.DetectedCount, report.Summary.ScenarioCount),
			rateMetric("fault_recovery_rate", report.Summary.RecoveredCount, report.Summary.ScenarioCount),
			rateMetric("healthy_false_positive_rate", report.Summary.FalsePositives, report.Summary.FalsePositiveChecks),
			{Name: "mean_mttd_seconds", Value: report.Summary.MeanMTTDSeconds, Unit: "seconds"},
			{Name: "recommendations_created", Value: float64(report.Summary.RecommendationCount), Unit: "count"},
			{Name: "recommendations_applied", Value: float64(report.Summary.AppliedCount), Unit: "count"},
		},
		SourceRefs: []string{"fault_campaign"}, Blockers: blockers,
		ForbiddenOverclaims: []string{"不得称为真实生产事故或混沌工程覆盖", "不得把检测和恢复识别写成缓解成功率", "不得声称已自动降权或切流"},
	}
}

func buildReliabilityEvidenceStatement(report reliabilityeval.Report) InterviewEvidenceStatement {
	ready := reliabilityeval.ValidateReport(report) == nil
	status, blockers := "resume_ready", []string{}
	if !ready {
		status, blockers = "technical_gate_failed", []string{"reliability_contract_failed"}
	}
	return InterviewEvidenceStatement{
		ID: "agent_recovery_and_sse_cancel", Category: "agent_reliability", Title: "Agent Checkpoint 恢复与 SSE 取消传播", Status: status, ResumeMetricEligible: ready,
		Claim: fmt.Sprintf("隔离验收中 Agent 恢复 %d/%d、重复 Resume 执行 %d；SSE 取消传播 %d/%d，P95 %.3fms，结束活跃 Worker %d。", report.AgentRecovery.Recovered, report.AgentRecovery.Scenarios, report.AgentRecovery.DuplicateResumeExecutions, report.SSECancellation.CancellationObserved, report.SSECancellation.Streams, report.SSECancellation.P95PropagationMillis, report.SSECancellation.ActiveWorkersAfter),
		Metrics: []InterviewEvidenceMetric{
			rateMetric("agent_recovery_rate", report.AgentRecovery.Recovered, report.AgentRecovery.Scenarios),
			{Name: "duplicate_resume_executions", Value: float64(report.AgentRecovery.DuplicateResumeExecutions), Unit: "count"},
			rateMetric("sse_cancellation_rate", report.SSECancellation.CancellationObserved, report.SSECancellation.Streams),
			{Name: "cancel_propagation_p95", Value: report.SSECancellation.P95PropagationMillis, Unit: "ms"},
			{Name: "active_workers_after", Value: float64(report.SSECancellation.ActiveWorkersAfter), Unit: "count"},
		},
		SourceRefs: []string{"agent_reliability"}, Blockers: blockers,
		ForbiddenOverclaims: []string{"不得称为真实主机掉电恢复", "不得外推到外部模型连接池或网络分区", "goroutine 观测值不得包装为完整资源泄漏证明"},
	}
}

func buildMetricCatalogEvidenceStatement(report metriccatalog.CatalogReport) InterviewEvidenceStatement {
	ready := report.Passed && report.RequiredFamilyCount == report.RequiredPresentCount && report.ForbiddenLabelHits == 0 && report.ContractMismatchCount == 0
	status, blockers := "resume_ready", []string{}
	if !ready {
		status, blockers = "technical_gate_failed", []string{"metric_catalog_contract_failed"}
	}
	return InterviewEvidenceStatement{
		ID: "bounded_metric_catalog", Category: "observability", Title: "Prometheus 指标目录与标签基数治理", Status: status, ResumeMetricEligible: ready,
		Claim: fmt.Sprintf("运行时发现 %d 个指标族，核心契约 %d/%d；高基数标签命中 %d，保守序列预算 %d/%d。", report.FamilyCount, report.RequiredPresentCount, report.RequiredFamilyCount, report.ForbiddenLabelHits, report.MaxSeriesEstimate, report.SeriesBudget),
		Metrics: []InterviewEvidenceMetric{
			{Name: "metric_family_count", Value: float64(report.FamilyCount), Unit: "count"},
			rateMetric("required_metric_contract_coverage", report.RequiredPresentCount, report.RequiredFamilyCount),
			{Name: "forbidden_label_hits", Value: float64(report.ForbiddenLabelHits), Unit: "count"},
			rateMetric("series_budget_utilization", report.MaxSeriesEstimate, report.SeriesBudget),
		},
		SourceRefs: []string{"metric_catalog"}, Blockers: blockers,
		ForbiddenOverclaims: []string{"不得把注册完整说成所有生产信号都有样本", "序列数是保守估算而非 TSDB 实际活跃序列", "不得把无高基数标签说成无任何可观测性成本"},
	}
}

func buildGrafanaEvidenceStatement(snapshot observability.GrafanaRuntimeSnapshot) InterviewEvidenceStatement {
	ready := snapshot.Status == "ready" && snapshot.Dashboard.Passed && !snapshot.PublicExposure
	status, blockers := "resume_ready", []string{}
	if !ready {
		status, blockers = "technical_gate_failed", []string{"grafana_runtime_not_ready"}
	}
	return InterviewEvidenceStatement{
		ID: "private_grafana_dashboard", Category: "observability", Title: "私网 Grafana 反馈闭环看板", Status: status, ResumeMetricEligible: ready,
		Claim: fmt.Sprintf("Grafana %s 在容器私网运行，固定看板含 %d 个面板、%d 条查询、%d 个分组，公网暴露=%t。", snapshot.GrafanaVersion, snapshot.Dashboard.PanelCount, snapshot.Dashboard.QueryCount, len(snapshot.Dashboard.Groups), snapshot.PublicExposure),
		Metrics: []InterviewEvidenceMetric{
			{Name: "grafana_panel_count", Value: float64(snapshot.Dashboard.PanelCount), Unit: "count"},
			{Name: "grafana_query_count", Value: float64(snapshot.Dashboard.QueryCount), Unit: "count"},
			{Name: "grafana_group_count", Value: float64(len(snapshot.Dashboard.Groups)), Unit: "count"},
			{Name: "grafana_public_exposure", Value: boolMetric(snapshot.PublicExposure), Unit: "boolean"},
		},
		SourceRefs: []string{"grafana_runtime"}, Blockers: blockers,
		ForbiddenOverclaims: []string{"不得把有看板说成监控自动优化了质量", "不得公开 Grafana 管理端口或凭据", "面板数量不等于告警准确率"},
	}
}

func buildControlEvidenceStatement(audit controlrecommendation.AuditSnapshot) InterviewEvidenceStatement {
	applied, simulatedRecommended, simulatedBlocked := 0, 0, 0
	for _, item := range audit.Latest {
		if item.Applied {
			applied++
		}
		if item.Simulation && item.Status == controlrecommendation.StatusRecommended {
			simulatedRecommended++
		}
		if item.Simulation && item.Status == controlrecommendation.StatusBlocked {
			simulatedBlocked++
		}
	}
	ready := audit.Mode == controlrecommendation.ModeRecommendOnly && len(audit.Latest) > 0 && applied == 0 && simulatedRecommended > 0 && simulatedBlocked > 0
	status, blockers := "resume_ready", []string{}
	if !ready {
		status, blockers = "technical_gate_failed", []string{"recommend_only_acceptance_missing_or_invalid"}
	}
	return InterviewEvidenceStatement{
		ID: "recommend_only_control_guard", Category: "closed_loop", Title: "Recommend-only 控制器不可写边界", Status: status, ResumeMetricEligible: ready,
		Claim: fmt.Sprintf("控制审计累计 recommended=%d、blocked=%d；最近 %d 条中 Applied=%d，当前活动策略仍为 %s。", audit.Recommended, audit.Blocked, len(audit.Latest), applied, audit.ActivePolicy.Version),
		Metrics: []InterviewEvidenceMetric{
			{Name: "control_recommended_total", Value: float64(audit.Recommended), Unit: "count"},
			{Name: "control_blocked_total", Value: float64(audit.Blocked), Unit: "count"},
			{Name: "recent_control_applied", Value: float64(applied), Unit: "count"},
			{Name: "acceptance_recommended_present", Value: boolMetric(simulatedRecommended > 0), Unit: "boolean"},
			{Name: "acceptance_blocked_present", Value: boolMetric(simulatedBlocked > 0), Unit: "boolean"},
		},
		SourceRefs: []string{"recommend_only_controller"}, Blockers: blockers,
		ForbiddenOverclaims: []string{"不得称为已自动修改线上路由权重", "不得把验收 Fixture 当作生产异常", "不得把候选建议数量说成质量收益"},
	}
}

func buildHarnessEvolutionEvidenceStatement(input interviewEvidenceInputs) InterviewEvidenceStatement {
	evolutionComparison, evolutionOK := harnessEvolvedComparison(input.EvolutionComparison, evolution.SplitEvolution)
	validationComparison, validationOK := harnessEvolvedComparison(input.EvolutionComparison, evolution.SplitValidation)
	promotionRejected, approvalBlocked := false, false
	for _, attempt := range input.EvolutionPromotion.Latest {
		promotionRejected = promotionRejected || attempt.RequestedDecision == evolution.PromotionDecisionReject && attempt.Outcome == evolution.PromotionOutcomeRecorded
		approvalBlocked = approvalBlocked || attempt.RequestedDecision == evolution.PromotionDecisionApprove && attempt.Outcome == evolution.PromotionOutcomeBlocked
	}
	shadowCandidateBlocked, rollbackBlocked := false, false
	for _, event := range input.EvolutionShadow.Latest {
		shadowCandidateBlocked = shadowCandidateBlocked || event.Operation == evolution.ControlOperationShadow && event.Outcome == evolution.ControlOutcomeBlocked && event.ReasonCode == "controlled_fixture_not_promotable"
		rollbackBlocked = rollbackBlocked || event.Operation == evolution.ControlOperationRollback && event.Outcome == evolution.ControlOutcomeBlocked && event.ReasonCode == "rollback_unavailable"
	}
	ready := evolutionOK && validationOK && input.EvolutionSplit.CoveredCases == input.EvolutionSplit.TotalCases && input.EvolutionSplit.OverlapCount == 0 &&
		input.EvolutionComparison.Promotion.Decision == "rejected" && !input.EvolutionComparison.Promotion.Eligible && !input.EvolutionComparison.Candidate.ProductionCandidate && input.EvolutionComparison.Holdout.OpenCount == 0 &&
		input.EvolutionControl.PassedCount == input.EvolutionControl.CaseCount && input.EvolutionControl.ProductionWrites == 0 && input.EvolutionControl.ProductionActivePointers == 0 &&
		promotionRejected && approvalBlocked && input.EvolutionPromotion.ActivePointers == 0 && shadowCandidateBlocked && rollbackBlocked && input.EvolutionShadow.AppliedCount == 0 && len(input.EvolutionShadow.ActivePointers) == 0 && !input.EvolutionShadow.AffectsLiveTraffic
	status, blockers := "verified_negative_control_result", []string{}
	if !ready {
		status, blockers = "technical_gate_failed", []string{"harness_evolution_evidence_incomplete_or_inconsistent"}
	}
	metrics := []InterviewEvidenceMetric{
		{Name: "production_lineage_artifacts", Value: float64(input.EvolutionLineage.ArtifactCount), Unit: "count"},
		rateMetric("split_coverage", input.EvolutionSplit.CoveredCases, input.EvolutionSplit.TotalCases),
		{Name: "holdout_open_count", Value: float64(input.EvolutionComparison.Holdout.OpenCount), Unit: "count"},
		rateMetric("control_state_machine_acceptance", input.EvolutionControl.PassedCount, input.EvolutionControl.CaseCount),
		{Name: "human_rejections_recorded", Value: float64(input.EvolutionPromotion.RejectedCount), Unit: "count"},
		{Name: "approval_attempts_blocked", Value: float64(input.EvolutionPromotion.BlockedCount), Unit: "count"},
		{Name: "shadow_control_events_blocked", Value: float64(input.EvolutionShadow.BlockedCount), Unit: "count"},
		{Name: "isolated_shadow_active_pointers", Value: float64(len(input.EvolutionShadow.ActivePointers)), Unit: "count"},
	}
	if evolutionOK {
		pValue := evolutionComparison.Analysis.McNemarExactTwoSidedPValue
		metrics = append(metrics, InterviewEvidenceMetric{Name: "evolution_candidate_mean_delta", Value: evolutionComparison.Analysis.MeanDelta, Unit: "ratio", CI95: &InterviewEvidenceInterval{Lower: evolutionComparison.Analysis.DeltaCI95Lower, Upper: evolutionComparison.Analysis.DeltaCI95Upper}, PValue: &pValue})
	}
	if validationOK {
		pValue := validationComparison.Analysis.McNemarExactTwoSidedPValue
		metrics = append(metrics, InterviewEvidenceMetric{Name: "validation_candidate_mean_delta", Value: validationComparison.Analysis.MeanDelta, Unit: "ratio", CI95: &InterviewEvidenceInterval{Lower: validationComparison.Analysis.DeltaCI95Lower, Upper: validationComparison.Analysis.DeltaCI95Upper}, PValue: &pValue})
	}
	return InterviewEvidenceStatement{
		ID: "gated_harness_evolution_negative_result", Category: "harness_evolution", Title: "受门禁 Harness Evolution 的可复现负结果", Status: status, ResumeMetricEligible: ready,
		Claim:      fmt.Sprintf("固定 %d/%d/%d 三分区对受控候选四方同预算比较：Evolution Δ%.1f%%、Validation Δ%.1f%%，上游收益门拒绝候选且 Holdout 打开 %d 次；CAS/rollback 状态机 %d/%d 通过，真实控制面 blocked=%d、isolated pointer=%d、线上流量未改变。", evolution.EvolutionCaseCount, evolution.ValidationCaseCount, evolution.HoldoutCaseCount, evolutionComparison.Analysis.MeanDelta*100, validationComparison.Analysis.MeanDelta*100, input.EvolutionComparison.Holdout.OpenCount, input.EvolutionControl.PassedCount, input.EvolutionControl.CaseCount, input.EvolutionShadow.BlockedCount, len(input.EvolutionShadow.ActivePointers)),
		Metrics:    metrics,
		SourceRefs: []string{"harness_evolution_lineage", "harness_evolution_split", "harness_evolution_comparison", "harness_control_acceptance", "harness_promotion_audit", "harness_shadow_control"}, Blockers: blockers,
		ForbiddenOverclaims: []string{"不得称自动候选提升了质量或自进化成功", "不得称 Sealed Holdout 已执行或得到泛化收益", "不得把受控 Fixture 冒充生产失败池候选", "不得把隔离 Shadow 指针说成生产路由切流"},
	}
}

func harnessEvolvedComparison(report evolution.ComparisonReport, splitName string) (evolution.NamedEvolutionComparison, bool) {
	for _, split := range report.Splits {
		if split.Split != splitName {
			continue
		}
		for _, comparison := range split.Comparisons {
			if comparison.CandidateVariant == evolution.VariantEvolved {
				return comparison, true
			}
		}
	}
	return evolution.NamedEvolutionComparison{}, false
}

func harnessPromotionGeneratedAt(audit evolution.PromotionAudit, fallback time.Time) time.Time {
	latest := fallback.UTC()
	for _, attempt := range audit.Latest {
		if attempt.CreatedAt.After(latest) {
			latest = attempt.CreatedAt.UTC()
		}
	}
	return latest
}

func harnessShadowGeneratedAt(audit evolution.ShadowControlAudit, fallback time.Time) time.Time {
	latest := fallback.UTC()
	for _, event := range audit.Latest {
		if event.CreatedAt.After(latest) {
			latest = event.CreatedAt.UTC()
		}
	}
	return latest
}

func controlEvidenceGeneratedAt(audit controlrecommendation.AuditSnapshot, fallback time.Time) time.Time {
	latest := fallback.UTC()
	for _, item := range audit.Latest {
		if item.CreatedAt.After(latest) {
			latest = item.CreatedAt.UTC()
		}
	}
	return latest
}

func boolMetric(value bool) float64 {
	if value {
		return 1
	}
	return 0
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
	if report == nil || report.SchemaVersion != interviewEvidenceSchemaVersion || report.ReleaseID == "" || len(report.GitSHA) != 40 || !report.AllSourcesVerified || report.TotalClaims != len(report.Statements) || report.ResumeReadyClaims < 0 || report.ResumeReadyClaims > report.TotalClaims || len(report.Sources) < 18 || len(report.Statements) != 11 {
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
