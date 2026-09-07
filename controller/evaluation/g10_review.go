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
	"net/http"
	"sort"
	"strings"
	"time"

	"GopherAI/common/mysql"
	"GopherAI/internal/catalogreview"
	"GopherAI/internal/catalogseal"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const g10ReviewSchemaVersion = "g10-release-review-v3"

type G10ReviewGate struct {
	ID                       string   `json:"id"`
	Title                    string   `json:"title"`
	Status                   string   `json:"status"`
	Conclusion               string   `json:"conclusion"`
	EvidenceRefs             []string `json:"evidence_refs"`
	NextAction               string   `json:"next_action"`
	UserConfirmationRequired bool     `json:"user_confirmation_required"`
}

type G10ResumeFact struct {
	StatementID        string   `json:"statement_id"`
	Title              string   `json:"title"`
	Claim              string   `json:"claim"`
	SourceRefs         []string `json:"source_refs"`
	RequiredQualifiers []string `json:"required_qualifiers"`
}

type G10ExcludedFact struct {
	StatementID string   `json:"statement_id"`
	Title       string   `json:"title"`
	Status      string   `json:"status"`
	Blockers    []string `json:"blockers"`
}

type G10DeferredItem struct {
	ID            string `json:"id"`
	ReasonCode    string `json:"reason_code"`
	Decision      string `json:"decision"`
	ResumeTrigger string `json:"resume_trigger"`
}

type G10CatalogReviewProgress struct {
	DatasetVersion                string `json:"dataset_version"`
	CatalogSHA256                 string `json:"catalog_sha256"`
	ReviewSetSHA256               string `json:"review_set_sha256"`
	Status                        string `json:"status"`
	Reviewed                      int    `json:"reviewed"`
	Total                         int    `json:"total"`
	Approved                      int    `json:"approved"`
	Rejected                      int    `json:"rejected"`
	Pending                       int    `json:"pending"`
	ReadyForSealedMaterialization bool   `json:"ready_for_sealed_materialization"`
}

type G10JudgeReviewProgress struct {
	Status                string  `json:"status"`
	Reviewed              int     `json:"reviewed"`
	Total                 int     `json:"total"`
	LinearWeightedKappa   float64 `json:"linear_weighted_kappa"`
	KappaAvailable        bool    `json:"kappa_available"`
	CalibrationGatePassed bool    `json:"calibration_gate_passed"`
}

type G10CatalogSealingProgress struct {
	Status                    string `json:"status"`
	Eligible                  bool   `json:"eligible"`
	CandidateReady            bool   `json:"candidate_ready"`
	ArtifactIntegrityVerified bool   `json:"artifact_integrity_verified"`
	SealID                    string `json:"seal_id,omitempty"`
	SealSHA256                string `json:"seal_sha256,omitempty"`
	OutputCatalogSHA256       string `json:"output_catalog_sha256,omitempty"`
	OutputReviewSHA256        string `json:"output_review_manifest_sha256,omitempty"`
	NextGate                  string `json:"next_gate"`
}

type G10HumanGateProgress struct {
	CatalogReview    G10CatalogReviewProgress  `json:"catalog_review"`
	CatalogSealing   G10CatalogSealingProgress `json:"catalog_sealing"`
	JudgeCalibration G10JudgeReviewProgress    `json:"judge_calibration"`
	SealedBaseline   bool                      `json:"sealed_baseline"`
	NextRequiredGate string                    `json:"next_required_gate"`
}

type G10ReleaseReview struct {
	SchemaVersion          string                     `json:"schema_version"`
	ReportSHA256           string                     `json:"report_sha256"`
	GeneratedAt            time.Time                  `json:"generated_at"`
	ReleaseID              string                     `json:"release_id"`
	GitSHA                 string                     `json:"git_sha"`
	EvidencePackageSHA256  string                     `json:"evidence_package_sha256"`
	ResumeFactSetSHA256    string                     `json:"resume_fact_set_sha256"`
	Status                 string                     `json:"status"`
	ProductionReleaseReady bool                       `json:"production_release_ready"`
	PassedGates            int                        `json:"passed_gates"`
	TotalGates             int                        `json:"total_gates"`
	ResumeFactCount        int                        `json:"resume_fact_count"`
	ExcludedFactCount      int                        `json:"excluded_fact_count"`
	Gates                  []G10ReviewGate            `json:"gates"`
	ResumeFacts            []G10ResumeFact            `json:"resume_facts"`
	ResumeConfirmation     *G10ResumeConfirmationView `json:"resume_confirmation,omitempty"`
	HumanGateProgress      *G10HumanGateProgress      `json:"human_gate_progress,omitempty"`
	ExcludedFacts          []G10ExcludedFact          `json:"excluded_facts"`
	DeferredItems          []G10DeferredItem          `json:"deferred_items"`
	Guardrails             []string                   `json:"guardrails"`
}

type G10ReviewService interface {
	Build(context.Context, string) (G10ReleaseReview, error)
	ConfirmResumeFacts(context.Context, string, G10ResumeConfirmationCommand) (G10ResumeConfirmationReceipt, error)
}

type evidenceBackedG10ReviewService struct {
	evidence      InterviewEvidenceService
	confirmations G10ResumeConfirmationStore
	catalogReview G10CatalogReviewProgressSource
	catalogSeal   G10CatalogSealStatusSource
	clock         func() time.Time
}

type G10CatalogReviewProgressSource interface {
	List(context.Context, string, catalogreview.Query) (catalogreview.Workbench, error)
}

type G10CatalogSealStatusSource interface {
	Status(context.Context, string) (catalogseal.Status, error)
}

func NewG10ReviewService(evidence InterviewEvidenceService) G10ReviewService {
	return &evidenceBackedG10ReviewService{evidence: evidence, clock: time.Now}
}

func newDefaultG10ReviewService() G10ReviewService {
	service := newG10ReviewService(newDefaultInterviewEvidenceService(), NewGormG10ResumeConfirmationStore(mysql.DB), time.Now)
	reviews := catalogreview.NewService(
		catalogreview.NewFileArtifactStore(catalogreview.DefaultManifestPath),
		catalogreview.NewGormRepository(mysql.DB), time.Now,
	)
	service.catalogReview = reviews
	service.catalogSeal = catalogseal.NewService(reviews, catalogseal.DefaultCatalogPath, catalogseal.DefaultReviewManifestPath, catalogseal.DefaultOutputRoot, time.Now)
	return service
}

func newG10ReviewService(evidence InterviewEvidenceService, confirmations G10ResumeConfirmationStore, clock func() time.Time) *evidenceBackedG10ReviewService {
	if clock == nil {
		clock = time.Now
	}
	return &evidenceBackedG10ReviewService{evidence: evidence, confirmations: confirmations, clock: clock}
}

func (service *evidenceBackedG10ReviewService) Build(ctx context.Context, principal string) (G10ReleaseReview, error) {
	if service == nil || service.evidence == nil {
		return G10ReleaseReview{}, errors.New("interview evidence service is required")
	}
	packageReport, err := service.evidence.Build(ctx, principal)
	if err != nil {
		return G10ReleaseReview{}, err
	}
	report, err := buildG10ReleaseReview(packageReport)
	if err != nil {
		return report, err
	}
	if service.catalogReview != nil {
		workbench, listErr := service.catalogReview.List(ctx, principal, catalogreview.Query{Status: "all", Page: 1, PageSize: 1})
		if listErr != nil {
			return G10ReleaseReview{}, listErr
		}
		var sealStatus *catalogseal.Status
		if service.catalogSeal != nil {
			current, statusErr := service.catalogSeal.Status(ctx, principal)
			if statusErr != nil {
				return G10ReleaseReview{}, statusErr
			}
			sealStatus = &current
		}
		if err := applyG10HumanGateProgress(&report, packageReport, workbench, sealStatus); err != nil {
			return G10ReleaseReview{}, err
		}
	}
	if service.confirmations == nil || strings.TrimSpace(principal) == "" {
		return report, nil
	}
	stored, found, err := service.confirmations.LatestForReviewer(ctx, g10ReviewerHash(principal))
	if err != nil {
		return G10ReleaseReview{}, err
	}
	if found {
		if err := applyG10ResumeConfirmation(&report, stored); err != nil {
			return G10ReleaseReview{}, err
		}
	}
	return report, nil
}

func applyG10HumanGateProgress(report *G10ReleaseReview, evidence InterviewEvidencePackage, workbench catalogreview.Workbench, sealStatus *catalogseal.Status) error {
	if report == nil || workbench.SchemaVersion != catalogreview.SchemaVersion || workbench.Progress.Total <= 0 || workbench.Progress.Total != workbench.Progress.Reviewed+workbench.Progress.Pending || workbench.Progress.Reviewed != workbench.Progress.Approved+workbench.Progress.Rejected || len(workbench.CatalogSHA256) != 64 || len(workbench.Progress.ReviewSetSHA256) != 64 {
		return errors.New("G10 catalog review progress is invalid")
	}
	judgeStatement, found := interviewStatementByID(evidence, "judge_human_calibration")
	if !found {
		return errors.New("G10 judge progress source is missing")
	}
	fullStatement, found := interviewStatementByID(evidence, "full_320_evaluation")
	if !found {
		return errors.New("G10 Full 320 progress source is missing")
	}
	judgeReviewed, judgeTotal, found := interviewRateCounts(judgeStatement, "human_review_completion")
	if !found || judgeTotal <= 0 || judgeReviewed < 0 || judgeReviewed > judgeTotal {
		return errors.New("G10 judge review progress is invalid")
	}
	kappa, found := interviewMetricValue(judgeStatement, "linear_weighted_kappa")
	if !found {
		return errors.New("G10 judge kappa source is missing")
	}
	sealing := G10CatalogSealingProgress{
		Status: "blocked_human_review", Eligible: workbench.Progress.ReadyForMaterializing,
		NextGate: "完成 320 条逐例人工复核且退回数为 0 后，创建不可变封存候选。",
	}
	if workbench.Progress.ReadyForMaterializing {
		sealing.Status, sealing.NextGate = "ready_for_seal", "显式确认后创建不可变封存候选；候选仍须重跑五类评测与统一 Runner。"
	}
	if sealStatus != nil {
		if err := validateG10SealStatus(*sealStatus, workbench); err != nil {
			return err
		}
		sealing.Status, sealing.Eligible, sealing.NextGate = sealStatus.Status, sealStatus.Eligible, sealStatus.NextGate
		if sealStatus.CurrentSeal != nil {
			sealing.CandidateReady, sealing.ArtifactIntegrityVerified = true, true
			sealing.SealID, sealing.SealSHA256 = sealStatus.CurrentSeal.SealID, sealStatus.CurrentSeal.SealSHA256
			sealing.OutputCatalogSHA256, sealing.OutputReviewSHA256 = sealStatus.CurrentSeal.OutputCatalogSHA256, sealStatus.CurrentSeal.OutputReviewSHA256
		}
	}
	progress := &G10HumanGateProgress{
		CatalogReview: G10CatalogReviewProgress{
			DatasetVersion: workbench.DatasetVersion, CatalogSHA256: workbench.CatalogSHA256,
			ReviewSetSHA256: workbench.Progress.ReviewSetSHA256, Status: workbench.Status,
			Reviewed: workbench.Progress.Reviewed, Total: workbench.Progress.Total, Approved: workbench.Progress.Approved,
			Rejected: workbench.Progress.Rejected, Pending: workbench.Progress.Pending,
			ReadyForSealedMaterialization: workbench.Progress.ReadyForMaterializing,
		},
		CatalogSealing: sealing,
		JudgeCalibration: G10JudgeReviewProgress{
			Status: judgeStatement.Status, Reviewed: judgeReviewed, Total: judgeTotal, LinearWeightedKappa: kappa,
			KappaAvailable: judgeReviewed == judgeTotal, CalibrationGatePassed: judgeStatement.ResumeMetricEligible,
		},
		SealedBaseline:   fullStatement.ResumeMetricEligible,
		NextRequiredGate: sealing.NextGate,
	}
	if sealing.CandidateReady {
		progress.NextRequiredGate = "封存候选完整性已复验；必须从候选目录重跑五类评测与统一 Runner，并经基线审批后才可标记 sealed baseline。"
	}
	if progress.SealedBaseline && progress.JudgeCalibration.CalibrationGatePassed {
		progress.NextRequiredGate = "产品人工门已由当前证据包验证；继续执行仍未完成的用户确认与隔离生产环境门。"
	}
	report.HumanGateProgress = progress
	for index := range report.Gates {
		if report.Gates[index].ID != "product_total_acceptance" {
			continue
		}
		if report.Gates[index].Status == "passed" {
			report.Gates[index].Conclusion = fmt.Sprintf("Full 320 当前账号已复核 %d/%d，正式基线已封存；Judge 已人工评分 %d/%d 且校准门通过。", progress.CatalogReview.Reviewed, progress.CatalogReview.Total, progress.JudgeCalibration.Reviewed, progress.JudgeCalibration.Total)
		} else {
			report.Gates[index].Conclusion = fmt.Sprintf("Full 320 当前账号已复核 %d/%d（通过 %d、退回 %d）；Judge 已人工评分 %d/%d。独立封存与统一重跑尚未完成。", progress.CatalogReview.Reviewed, progress.CatalogReview.Total, progress.CatalogReview.Approved, progress.CatalogReview.Rejected, progress.JudgeCalibration.Reviewed, progress.JudgeCalibration.Total)
			if progress.CatalogSealing.CandidateReady {
				report.Gates[index].Conclusion += fmt.Sprintf(" 不可变候选 %s 已通过完整性复验，但正式基线证据尚未重跑冻结。", progress.CatalogSealing.SealID)
			}
		}
		report.Gates[index].EvidenceRefs = []string{"catalog_review_queue", "catalog_sealed_candidate", "full_320_evaluation", "judge_human_calibration"}
		report.Gates[index].NextAction = progress.NextRequiredGate
	}
	return finalizeG10ReleaseReview(report)
}

func validateG10SealStatus(status catalogseal.Status, workbench catalogreview.Workbench) error {
	progress := status.Progress
	if status.SchemaVersion != catalogseal.StatusSchemaVersion || strings.TrimSpace(status.DatasetVersion) == "" || status.DatasetVersion != workbench.DatasetVersion || status.CatalogSHA256 != workbench.CatalogSHA256 || status.ReviewSetSHA != workbench.Progress.ReviewSetSHA256 || progress.ReviewSetSHA256 != workbench.Progress.ReviewSetSHA256 || progress.Total != workbench.Progress.Total || progress.Reviewed != workbench.Progress.Reviewed || progress.Approved != workbench.Progress.Approved || progress.Rejected != workbench.Progress.Rejected || progress.Pending != workbench.Progress.Pending || progress.ReadyForMaterializing != workbench.Progress.ReadyForMaterializing || status.Eligible != workbench.Progress.ReadyForMaterializing || strings.TrimSpace(status.NextGate) == "" {
		return errors.New("G10 catalog seal status is inconsistent")
	}
	switch status.Status {
	case "blocked_human_review":
		if status.Eligible || status.CurrentSeal != nil {
			return errors.New("G10 blocked seal status is invalid")
		}
	case "ready_for_seal":
		if !status.Eligible || status.CurrentSeal != nil {
			return errors.New("G10 ready seal status is invalid")
		}
	case "sealed_candidate_ready":
		current := status.CurrentSeal
		if !status.Eligible || current == nil || current.SchemaVersion != catalogseal.SchemaVersion || current.Status != "sealed_candidate_ready" || current.SourceCatalogSHA256 != status.CatalogSHA256 || current.SourceReviewSetSHA256 != status.ReviewSetSHA || current.CaseCount != 320 || current.ApprovedCases != 320 || len(current.SealID) != len("catalog-seal-")+32 || len(current.SealSHA256) != 64 || len(current.OutputCatalogSHA256) != 64 || len(current.OutputReviewSHA256) != 64 {
			return errors.New("G10 sealed candidate status is invalid")
		}
	default:
		return errors.New("G10 catalog seal status is unknown")
	}
	return nil
}

func interviewStatementByID(report InterviewEvidencePackage, id string) (InterviewEvidenceStatement, bool) {
	for _, statement := range report.Statements {
		if statement.ID == id {
			return statement, true
		}
	}
	return InterviewEvidenceStatement{}, false
}

func interviewRateCounts(statement InterviewEvidenceStatement, name string) (int, int, bool) {
	for _, metric := range statement.Metrics {
		if metric.Name == name && metric.Numerator != nil && metric.Denominator != nil {
			return *metric.Numerator, *metric.Denominator, true
		}
	}
	return 0, 0, false
}

func interviewMetricValue(statement InterviewEvidenceStatement, name string) (float64, bool) {
	for _, metric := range statement.Metrics {
		if metric.Name == name {
			return metric.Value, true
		}
	}
	return 0, false
}

func buildG10ReleaseReview(evidence InterviewEvidencePackage) (G10ReleaseReview, error) {
	if err := verifyInterviewEvidencePackage(evidence); err != nil {
		return G10ReleaseReview{}, err
	}
	statements := make(map[string]InterviewEvidenceStatement, len(evidence.Statements))
	resumeFacts := make([]G10ResumeFact, 0, evidence.ResumeReadyClaims)
	excludedFacts := make([]G10ExcludedFact, 0, evidence.TotalClaims-evidence.ResumeReadyClaims)
	for _, statement := range evidence.Statements {
		statements[statement.ID] = statement
		if statement.ResumeMetricEligible {
			resumeFacts = append(resumeFacts, G10ResumeFact{
				StatementID: statement.ID, Title: statement.Title, Claim: statement.Claim,
				SourceRefs:         append([]string(nil), statement.SourceRefs...),
				RequiredQualifiers: append([]string(nil), statement.ForbiddenOverclaims...),
			})
			continue
		}
		excludedFacts = append(excludedFacts, G10ExcludedFact{
			StatementID: statement.ID, Title: statement.Title, Status: statement.Status,
			Blockers: append([]string(nil), statement.Blockers...),
		})
	}
	sort.Slice(resumeFacts, func(i, j int) bool { return resumeFacts[i].StatementID < resumeFacts[j].StatementID })
	sort.Slice(excludedFacts, func(i, j int) bool { return excludedFacts[i].StatementID < excludedFacts[j].StatementID })

	fullEvaluation, hasFullEvaluation := statements["full_320_evaluation"]
	judgeCalibration, hasJudgeCalibration := statements["judge_human_calibration"]
	cleanup, hasCleanup := statements["audit_driven_source_cleanup"]
	if !hasFullEvaluation || !hasJudgeCalibration || !hasCleanup {
		return G10ReleaseReview{}, errors.New("required G10 evidence statements are missing")
	}
	productGatePassed := fullEvaluation.ResumeMetricEligible && judgeCalibration.ResumeMetricEligible
	productStatus, productConclusion := "blocked", "Full 320 人工标签与 Judge 人工一致性门尚未全部完成，产品总验收不能冻结。"
	if productGatePassed {
		productStatus, productConclusion = "passed", "Full 320 与 Judge 人工门均已由当前证据包验证。"
	}
	cleanupStatus, cleanupConclusion := "blocked", "删除清单或恢复边界未形成当前 Release 的完整证据。"
	if cleanup.ResumeMetricEligible {
		cleanupStatus, cleanupConclusion = "passed", "删除项、保留运行边界和 Git 恢复路径已由当前清理审计验证。"
	}
	gates := []G10ReviewGate{
		{ID: "product_total_acceptance", Title: "产品总验收", Status: productStatus, Conclusion: productConclusion, EvidenceRefs: []string{"full_320_evaluation", "judge_human_calibration"}, NextAction: "完成 320 条人工标签复核与 30 条 Judge 人工校准后重新生成证据包。"},
		{ID: "resume_fact_confirmation", Title: "简历数字人工确认", Status: "pending_user", Conclusion: fmt.Sprintf("%d 条事实通过技术证据门，但尚未记录用户对最终简历表述的确认。", len(resumeFacts)), EvidenceRefs: resumeFactIDs(resumeFacts), NextAction: "用户从事实清单中选择最终 3～5 条表述并确认保留限定语。", UserConfirmationRequired: true},
		{ID: "production_rollback_drill", Title: "生产回滚演练", Status: "deferred_environment", Conclusion: "当前单 ECS、单应用容器不具备不影响唯一在线实例的生产回滚演练窗口。", EvidenceRefs: []string{"current_release_manifest"}, NextAction: "建立隔离 staging 或第二实例后，执行镜像、策略与 Schema 联合回滚演练。"},
		{ID: "cleanup_manifest_and_recovery", Title: "删除清单与恢复说明", Status: cleanupStatus, Conclusion: cleanupConclusion, EvidenceRefs: []string{"audit_driven_source_cleanup"}, NextAction: "继续用零调用窗口和当前 Release 源码清单复核，不删除 MCP 协议宿主与退役观测哨兵。"},
	}
	passed := 0
	for _, gate := range gates {
		if gate.Status == "passed" {
			passed++
		}
	}
	generatedAt, err := interviewReleaseGeneratedAt(evidence)
	if err != nil {
		return G10ReleaseReview{}, err
	}
	report := G10ReleaseReview{
		SchemaVersion: g10ReviewSchemaVersion, GeneratedAt: generatedAt, ReleaseID: evidence.ReleaseID,
		GitSHA: evidence.GitSHA, EvidencePackageSHA256: evidence.PackageSHA256, ResumeFactSetSHA256: g10ResumeFactSetHash(resumeFacts),
		Status: "g10_blocked_by_human_and_environment_gates", ProductionReleaseReady: false,
		PassedGates: passed, TotalGates: len(gates), ResumeFactCount: len(resumeFacts), ExcludedFactCount: len(excludedFacts),
		Gates: gates, ResumeFacts: resumeFacts, ExcludedFacts: excludedFacts,
		DeferredItems: []G10DeferredItem{
			{ID: "contract_database_migration", ReasonCode: "no_isolated_staging_or_old_version_traffic_proof", Decision: "保留现有兼容 Schema，不执行收缩迁移。", ResumeTrigger: "具备备份恢复验证、隔离 staging 和旧版本零流量证明。"},
			{ID: "percentage_production_rollout", ReasonCode: "single_instance_has_no_honest_traffic_split", Decision: "不伪造 5%→20%→50%→100% 生产灰度。", ResumeTrigger: "具备至少两个隔离运行单元和可归因流量分桶。"},
		},
		Guardrails: []string{"technical_evidence_does_not_equal_user_resume_approval", "pending_human_review_blocks_product_gate", "negative_results_remain_visible", "single_instance_rollout_not_claimed", "production_rollback_not_simulated", "source_hash_and_release_binding_required"},
	}
	if productGatePassed && cleanup.ResumeMetricEligible {
		report.Status = "g10_pending_user_and_environment_gates"
	}
	if err := finalizeG10ReleaseReview(&report); err != nil {
		return G10ReleaseReview{}, err
	}
	return report, nil
}

func resumeFactIDs(facts []G10ResumeFact) []string {
	ids := make([]string, 0, len(facts))
	for _, fact := range facts {
		ids = append(ids, fact.StatementID)
	}
	return ids
}

func g10ResumeFactSetHash(facts []G10ResumeFact) string {
	encoded, _ := json.Marshal(facts)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func interviewReleaseGeneratedAt(evidence InterviewEvidencePackage) (time.Time, error) {
	for _, source := range evidence.Sources {
		if source.Name == "current_release_manifest" && !source.GeneratedAt.IsZero() {
			return source.GeneratedAt.UTC(), nil
		}
	}
	return time.Time{}, errors.New("current release source is missing")
}

func verifyInterviewEvidencePackage(report InterviewEvidencePackage) error {
	expected := report.PackageSHA256
	copyReport := report
	if err := finalizeInterviewEvidencePackage(&copyReport); err != nil {
		return err
	}
	if expected == "" || copyReport.PackageSHA256 != expected {
		return errors.New("interview evidence package hash mismatch")
	}
	return nil
}

func finalizeG10ReleaseReview(report *G10ReleaseReview) error {
	if report == nil || report.SchemaVersion != g10ReviewSchemaVersion || report.ReleaseID == "" || len(report.GitSHA) != 40 || len(report.EvidencePackageSHA256) != 64 || len(report.ResumeFactSetSHA256) != 64 || report.GeneratedAt.IsZero() || report.ProductionReleaseReady || report.TotalGates != len(report.Gates) || report.ResumeFactCount != len(report.ResumeFacts) || report.ExcludedFactCount != len(report.ExcludedFacts) || len(report.Gates) != 4 || len(report.DeferredItems) != 2 || len(report.Guardrails) == 0 {
		return errors.New("G10 release review is invalid")
	}
	passed, gateIDs := 0, map[string]struct{}{}
	for _, gate := range report.Gates {
		if gate.ID == "" || gate.Title == "" || gate.Conclusion == "" || gate.NextAction == "" || len(gate.EvidenceRefs) == 0 {
			return errors.New("G10 gate is incomplete")
		}
		if _, duplicate := gateIDs[gate.ID]; duplicate {
			return errors.New("G10 gate ids must be unique")
		}
		gateIDs[gate.ID] = struct{}{}
		switch gate.Status {
		case "passed":
			passed++
		case "blocked", "pending_user", "deferred_environment":
		default:
			return errors.New("G10 gate status is invalid")
		}
	}
	if passed != report.PassedGates || passed == report.TotalGates {
		return errors.New("G10 gate accounting is inconsistent")
	}
	factIDs := map[string]struct{}{}
	for _, fact := range report.ResumeFacts {
		if fact.StatementID == "" || fact.Title == "" || fact.Claim == "" || len(fact.SourceRefs) == 0 || len(fact.RequiredQualifiers) == 0 {
			return errors.New("G10 resume fact is incomplete")
		}
		if _, duplicate := factIDs[fact.StatementID]; duplicate {
			return errors.New("G10 fact ids must be unique")
		}
		factIDs[fact.StatementID] = struct{}{}
	}
	for _, fact := range report.ExcludedFacts {
		if fact.StatementID == "" || fact.Title == "" || fact.Status == "" {
			return errors.New("G10 excluded fact is incomplete")
		}
		if _, duplicate := factIDs[fact.StatementID]; duplicate {
			return errors.New("G10 fact ids must be unique")
		}
		factIDs[fact.StatementID] = struct{}{}
	}
	if len(factIDs) != report.ResumeFactCount+report.ExcludedFactCount {
		return errors.New("G10 fact accounting is inconsistent")
	}
	if report.ResumeFactSetSHA256 != g10ResumeFactSetHash(report.ResumeFacts) {
		return errors.New("G10 resume fact set hash is inconsistent")
	}
	if report.ResumeConfirmation != nil {
		confirmation := report.ResumeConfirmation
		if len(confirmation.ConfirmationSHA256) != 64 || len(confirmation.FactSetSHA256) != 64 || confirmation.SelectedCount != len(confirmation.SelectedFactIDs) || confirmation.SelectedCount < 3 || confirmation.SelectedCount > 5 || confirmation.CreatedAt.IsZero() || (confirmation.CurrentBinding && confirmation.FactSetSHA256 != report.ResumeFactSetSHA256) {
			return errors.New("G10 resume confirmation view is invalid")
		}
	}
	if progress := report.HumanGateProgress; progress != nil {
		catalog := progress.CatalogReview
		sealing := progress.CatalogSealing
		judge := progress.JudgeCalibration
		if catalog.Total <= 0 || catalog.Reviewed < 0 || catalog.Pending < 0 || catalog.Approved < 0 || catalog.Rejected < 0 || catalog.Reviewed+catalog.Pending != catalog.Total || catalog.Approved+catalog.Rejected != catalog.Reviewed || len(catalog.CatalogSHA256) != 64 || len(catalog.ReviewSetSHA256) != 64 || strings.TrimSpace(catalog.DatasetVersion) == "" || strings.TrimSpace(catalog.Status) == "" || sealing.Eligible != catalog.ReadyForSealedMaterialization || strings.TrimSpace(sealing.Status) == "" || strings.TrimSpace(sealing.NextGate) == "" || sealing.CandidateReady != sealing.ArtifactIntegrityVerified || judge.Total <= 0 || judge.Reviewed < 0 || judge.Reviewed > judge.Total || judge.KappaAvailable != (judge.Reviewed == judge.Total) || strings.TrimSpace(judge.Status) == "" || strings.TrimSpace(progress.NextRequiredGate) == "" {
			return errors.New("G10 human gate progress is invalid")
		}
		if sealing.CandidateReady {
			if !sealing.Eligible || sealing.Status != "sealed_candidate_ready" || len(sealing.SealID) != len("catalog-seal-")+32 || len(sealing.SealSHA256) != 64 || len(sealing.OutputCatalogSHA256) != 64 || len(sealing.OutputReviewSHA256) != 64 {
				return errors.New("G10 catalog sealing progress is invalid")
			}
		} else if sealing.SealID != "" || sealing.SealSHA256 != "" || sealing.OutputCatalogSHA256 != "" || sealing.OutputReviewSHA256 != "" {
			return errors.New("G10 unsealed progress contains artifact identity")
		} else if (sealing.Status == "blocked_human_review" && sealing.Eligible) || (sealing.Status == "ready_for_seal" && !sealing.Eligible) || (sealing.Status != "blocked_human_review" && sealing.Status != "ready_for_seal") {
			return errors.New("G10 catalog sealing state is invalid")
		}
	}
	report.ReportSHA256 = ""
	encoded, err := json.Marshal(report)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	report.ReportSHA256 = hex.EncodeToString(digest[:])
	return nil
}

func writeG10ReviewMarkdown(writer io.Writer, report G10ReleaseReview) error {
	if writer == nil || len(report.ReportSHA256) != 64 {
		return errors.New("valid G10 release review is required")
	}
	if _, err := fmt.Fprintf(writer, "# GopherAI G10 发布事实核验\n\n- Release：`%s`\n- Git SHA：`%s`\n- Evidence Package：`%s`\n- Resume Fact Set：`%s`\n- Report SHA-256：`%s`\n- G10 状态：`%s`\n- 生产发布总门：`%t`\n- 通过门：`%d/%d`\n\n", report.ReleaseID, report.GitSHA, report.EvidencePackageSHA256, report.ResumeFactSetSHA256, report.ReportSHA256, report.Status, report.ProductionReleaseReady, report.PassedGates, report.TotalGates); err != nil {
		return err
	}
	if _, err := io.WriteString(writer, "## G10 Gate\n\n"); err != nil {
		return err
	}
	for _, gate := range report.Gates {
		if _, err := fmt.Fprintf(writer, "- **%s**：`%s`。%s 下一步：%s\n", gate.Title, gate.Status, gate.Conclusion, gate.NextAction); err != nil {
			return err
		}
	}
	if progress := report.HumanGateProgress; progress != nil {
		if _, err := fmt.Fprintf(writer, "\n## 人工门实时进度\n\n- Full 320：`%d/%d`，通过 `%d`，退回 `%d`，待审 `%d`，Review Set `%s`\n- 不可变封存候选：状态 `%s`，完整性复验 `%t`，Seal `%s`\n- Judge 30：`%d/%d`，κ `%s`，校准门 `%t`\n- 正式基线已封存：`%t`\n- 下一门：%s\n\n", progress.CatalogReview.Reviewed, progress.CatalogReview.Total, progress.CatalogReview.Approved, progress.CatalogReview.Rejected, progress.CatalogReview.Pending, progress.CatalogReview.ReviewSetSHA256, progress.CatalogSealing.Status, progress.CatalogSealing.ArtifactIntegrityVerified, emptyG10SealID(progress.CatalogSealing.SealID), progress.JudgeCalibration.Reviewed, progress.JudgeCalibration.Total, formatG10Kappa(progress.JudgeCalibration), progress.JudgeCalibration.CalibrationGatePassed, progress.SealedBaseline, progress.NextRequiredGate); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(writer, "\n## 当前通过技术证据门的事实\n\n"); err != nil {
		return err
	}
	for _, fact := range report.ResumeFacts {
		if _, err := fmt.Fprintf(writer, "### %s\n\n%s\n\n- Statement：`%s`\n- 来源：`%s`\n- 必须保留的边界：%s\n\n", fact.Title, fact.Claim, fact.StatementID, strings.Join(fact.SourceRefs, "`, `"), strings.Join(fact.RequiredQualifiers, "；")); err != nil {
			return err
		}
	}
	if report.ResumeConfirmation != nil {
		if _, err := fmt.Fprintf(writer, "## 用户事实确认\n\n- 状态：`%s`\n- 已选：`%d` 条\n- Fact Set 当前有效：`%t`\n- Confirmation SHA：`%s`\n\n", report.ResumeConfirmation.Status, report.ResumeConfirmation.SelectedCount, report.ResumeConfirmation.CurrentBinding, report.ResumeConfirmation.ConfirmationSHA256); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(writer, "## 当前禁止写入简历的事实\n\n"); err != nil {
		return err
	}
	for _, fact := range report.ExcludedFacts {
		if _, err := fmt.Fprintf(writer, "- **%s**：`%s`；阻塞 `%s`\n", fact.Title, fact.Status, strings.Join(fact.Blockers, "`, `")); err != nil {
			return err
		}
	}
	return nil
}

func formatG10Kappa(progress G10JudgeReviewProgress) string {
	if !progress.KappaAvailable {
		return "待满全部人工评分后计算"
	}
	return fmt.Sprintf("%.4f", progress.LinearWeightedKappa)
}

func emptyG10SealID(id string) string {
	if strings.TrimSpace(id) == "" {
		return "未生成"
	}
	return id
}

type G10ReviewHandler struct{ service G10ReviewService }

func NewG10ReviewHandler(service G10ReviewService) *G10ReviewHandler {
	return &G10ReviewHandler{service: service}
}

func NewDefaultG10ReviewHandler() *G10ReviewHandler {
	return NewG10ReviewHandler(newDefaultG10ReviewService())
}

func (handler *G10ReviewHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeG10ReviewError(ginContext, http.StatusServiceUnavailable, "G10_REVIEW_UNAVAILABLE", "G10 发布事实核验暂不可用")
		return
	}
	format := strings.TrimSpace(ginContext.Query("format"))
	if format != "" && format != "json" && format != "markdown" {
		writeG10ReviewError(ginContext, http.StatusBadRequest, "INVALID_G10_REVIEW_FORMAT", "仅支持 json 或 markdown 格式")
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 5*time.Second)
	defer cancel()
	report, err := handler.service.Build(ctx, ginContext.GetString("userName"))
	if err != nil {
		writeG10ReviewError(ginContext, http.StatusServiceUnavailable, "G10_REVIEW_NOT_READY", "当前 Release 的 G10 证据尚未全部就绪")
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.Header("X-Content-Type-Options", "nosniff")
	if format == "markdown" {
		var buffer bytes.Buffer
		if err := writeG10ReviewMarkdown(&buffer, report); err != nil {
			writeG10ReviewError(ginContext, http.StatusServiceUnavailable, "G10_REVIEW_EXPORT_FAILED", "G10 发布事实核验导出失败")
			return
		}
		ginContext.Header("Content-Disposition", `attachment; filename="gopherai-g10-release-review.md"`)
		ginContext.Data(http.StatusOK, "text/markdown; charset=utf-8", buffer.Bytes())
		return
	}
	if format == "json" {
		ginContext.Header("Content-Disposition", `attachment; filename="gopherai-g10-release-review.json"`)
	}
	ginContext.JSON(http.StatusOK, report)
}

func writeG10ReviewError(ginContext *gin.Context, status int, code, message string) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(status, ErrorResponse{SchemaVersion: g10ReviewSchemaVersion, Code: code, Message: message, Retryable: status >= 500, TraceID: traceID})
}
