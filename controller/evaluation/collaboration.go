package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	evaldomain "GopherAI/internal/evaluation"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const (
	collaborationResponseSchemaVersion = "collaboration-evaluation-summary-v1"
	defaultCollaborationReportPath     = "evals/results/devsupport-collaboration-ab-v1-candidate.json"
	maxCollaborationReportBytes        = 8 << 20
)

type CollaborationReportStore interface {
	Load(context.Context) (evaldomain.CollaborationABReport, string, error)
}

type FileCollaborationReportStore struct{ path string }

func NewFileCollaborationReportStore(path string) *FileCollaborationReportStore {
	return &FileCollaborationReportStore{path: strings.TrimSpace(path)}
}

func (store *FileCollaborationReportStore) Load(_ context.Context) (evaldomain.CollaborationABReport, string, error) {
	if store == nil || store.path == "" {
		return evaldomain.CollaborationABReport{}, "", fmt.Errorf("collaboration evaluation report path is required")
	}
	file, err := os.Open(store.path)
	if err != nil {
		return evaldomain.CollaborationABReport{}, "", err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxCollaborationReportBytes+1))
	if err != nil || len(encoded) == 0 || len(encoded) > maxCollaborationReportBytes {
		return evaldomain.CollaborationABReport{}, "", fmt.Errorf("collaboration evaluation report is unavailable")
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var report evaldomain.CollaborationABReport
	if err := decoder.Decode(&report); err != nil {
		return evaldomain.CollaborationABReport{}, "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return evaldomain.CollaborationABReport{}, "", fmt.Errorf("collaboration evaluation report has trailing content")
	}
	if err := validateCollaborationReport(report); err != nil {
		return evaldomain.CollaborationABReport{}, "", err
	}
	digest := sha256.Sum256(encoded)
	return report, hex.EncodeToString(digest[:]), nil
}

type CollaborationHandler struct{ store CollaborationReportStore }

type CollaborationSummaryResponse struct {
	SchemaVersion            string                            `json:"schema_version"`
	EvaluatorVersion         string                            `json:"evaluator_version"`
	DatasetVersion           string                            `json:"dataset_version"`
	CandidateVersion         string                            `json:"candidate_version"`
	GeneratedAt              time.Time                         `json:"generated_at"`
	ReportSHA256             string                            `json:"report_sha256"`
	HumanReviewed            bool                              `json:"human_reviewed"`
	BaselineEligible         bool                              `json:"baseline_eligible"`
	TechnicalGatesPassed     bool                              `json:"technical_gates_passed"`
	NetBenefitPassed         bool                              `json:"net_benefit_passed"`
	PromotionEligible        bool                              `json:"promotion_eligible"`
	DefaultTrafficEnabled    bool                              `json:"default_traffic_enabled"`
	RecommendedDefaultWeight int                               `json:"recommended_default_weight"`
	GateFailures             []string                          `json:"gate_failures,omitempty"`
	Metrics                  evaldomain.CollaborationABMetrics `json:"metrics"`
	Runtime                  evaldomain.CollaborationRuntime   `json:"runtime"`
	Limitations              []string                          `json:"limitations"`
}

func NewCollaborationHandler(store CollaborationReportStore) *CollaborationHandler {
	return &CollaborationHandler{store: store}
}

func NewDefaultCollaborationHandler() *CollaborationHandler {
	return NewCollaborationHandler(NewFileCollaborationReportStore(defaultCollaborationReportPath))
}

func (handler *CollaborationHandler) Latest(context *gin.Context) {
	if handler == nil || handler.store == nil {
		handler.writeError(context)
		return
	}
	report, reportSHA, err := handler.store.Load(context.Request.Context())
	if err != nil {
		handler.writeError(context)
		return
	}
	etag := `"` + reportSHA + `"`
	context.Header("ETag", etag)
	context.Header("Cache-Control", "private, max-age=30")
	if context.GetHeader("If-None-Match") == etag {
		context.Status(http.StatusNotModified)
		return
	}
	context.JSON(http.StatusOK, CollaborationSummaryResponse{
		SchemaVersion: collaborationResponseSchemaVersion, EvaluatorVersion: report.EvaluatorVersion,
		DatasetVersion: report.DatasetVersion, CandidateVersion: report.CandidateVersion, GeneratedAt: report.GeneratedAt,
		ReportSHA256: reportSHA, HumanReviewed: report.HumanReviewed, BaselineEligible: report.BaselineEligible,
		TechnicalGatesPassed: report.TechnicalGatesPassed, NetBenefitPassed: report.NetBenefitPassed,
		PromotionEligible: report.PromotionEligible, DefaultTrafficEnabled: report.DefaultTrafficEnabled,
		RecommendedDefaultWeight: report.RecommendedDefaultWeight, GateFailures: report.GateFailures,
		Metrics: report.Metrics, Runtime: report.Runtime,
		Limitations: []string{
			"10 条复杂目标与 10 条简单门禁标签仍待用户人工复核，当前只是一份技术候选报告。",
			"质量分是可复现的根因、证据和安全 rubric，不是 LLM-as-a-Judge，也不等同于线上解决率。",
			"外部回答模型可变，完成密封留出集复跑前，diagnosis_collaborative 默认权重固定为 0。",
		},
	})
}

func validateCollaborationReport(report evaldomain.CollaborationABReport) error {
	if report.SchemaVersion == "" || report.EvaluatorVersion != evaldomain.CollaborationEvaluatorVersion || report.DatasetVersion != evaldomain.CollaborationDatasetVersion || report.GeneratedAt.IsZero() {
		return fmt.Errorf("collaboration evaluation report metadata is inconsistent")
	}
	if report.Metrics.CaseCount != evaldomain.CollaborationCaseCount || report.Metrics.CaseCount != len(report.Cases) || report.Metrics.TargetCaseCount != evaldomain.CollaborationTargetCaseCount || report.Metrics.SimpleGuardCaseCount != evaldomain.CollaborationGuardCaseCount {
		return fmt.Errorf("collaboration evaluation case counts are inconsistent")
	}
	if report.BaselineEligible && (!report.HumanReviewed || !report.TechnicalGatesPassed) {
		return fmt.Errorf("collaboration baseline eligibility is inconsistent")
	}
	if report.PromotionEligible && (!report.BaselineEligible || !report.NetBenefitPassed) {
		return fmt.Errorf("collaboration promotion eligibility is inconsistent")
	}
	if report.DefaultTrafficEnabled || report.RecommendedDefaultWeight != 0 {
		return fmt.Errorf("unreviewed collaboration report cannot enable default traffic")
	}
	if report.TechnicalGatesPassed && len(report.GateFailures) > 0 {
		return fmt.Errorf("collaboration technical gate status is inconsistent")
	}
	return nil
}

func (handler *CollaborationHandler) writeError(context *gin.Context) {
	_, traceID := requestid.IDs(context)
	context.JSON(http.StatusServiceUnavailable, ErrorResponse{
		SchemaVersion: collaborationResponseSchemaVersion, Code: "COLLABORATION_EVALUATION_UNAVAILABLE",
		Message: "多 Agent 成对评测报告暂时不可用", Retryable: true, TraceID: traceID,
	})
}
