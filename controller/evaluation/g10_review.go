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
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const g10ReviewSchemaVersion = "g10-release-review-v1"

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
	clock         func() time.Time
}

func NewG10ReviewService(evidence InterviewEvidenceService) G10ReviewService {
	return &evidenceBackedG10ReviewService{evidence: evidence, clock: time.Now}
}

func newDefaultG10ReviewService() G10ReviewService {
	return newG10ReviewService(newDefaultInterviewEvidenceService(), NewGormG10ResumeConfirmationStore(mysql.DB), time.Now)
}

func newG10ReviewService(evidence InterviewEvidenceService, confirmations G10ResumeConfirmationStore, clock func() time.Time) G10ReviewService {
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
	if err != nil || service.confirmations == nil || strings.TrimSpace(principal) == "" {
		return report, err
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
