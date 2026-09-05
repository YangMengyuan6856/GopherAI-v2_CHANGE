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
	parentContextResponseSchemaVersion = "parent-context-evaluation-summary-v1"
	defaultParentContextReportPath     = "evals/results/devsupport-parent-context-ab-v1-candidate.json"
	maxParentContextReportBytes        = 8 << 20
)

type ParentContextReportStore interface {
	Load(context.Context) (evaldomain.ParentContextABReport, string, error)
}

type FileParentContextReportStore struct{ path string }

func NewFileParentContextReportStore(path string) *FileParentContextReportStore {
	return &FileParentContextReportStore{path: strings.TrimSpace(path)}
}

func (store *FileParentContextReportStore) Load(_ context.Context) (evaldomain.ParentContextABReport, string, error) {
	if store == nil || store.path == "" {
		return evaldomain.ParentContextABReport{}, "", fmt.Errorf("parent-context evaluation report path is required")
	}
	file, err := os.Open(store.path)
	if err != nil {
		return evaldomain.ParentContextABReport{}, "", err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxParentContextReportBytes+1))
	if err != nil || len(encoded) == 0 || len(encoded) > maxParentContextReportBytes {
		return evaldomain.ParentContextABReport{}, "", fmt.Errorf("parent-context evaluation report is unavailable")
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var report evaldomain.ParentContextABReport
	if err := decoder.Decode(&report); err != nil {
		return evaldomain.ParentContextABReport{}, "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return evaldomain.ParentContextABReport{}, "", fmt.Errorf("parent-context evaluation report has trailing content")
	}
	if err := validateParentContextReport(report); err != nil {
		return evaldomain.ParentContextABReport{}, "", err
	}
	digest := sha256.Sum256(encoded)
	return report, hex.EncodeToString(digest[:]), nil
}

type ParentContextHandler struct{ store ParentContextReportStore }

type ParentContextSummaryResponse struct {
	SchemaVersion            string                            `json:"schema_version"`
	EvaluatorVersion         string                            `json:"evaluator_version"`
	DatasetVersion           string                            `json:"dataset_version"`
	CandidateVersion         string                            `json:"candidate_version"`
	GeneratedAt              time.Time                         `json:"generated_at"`
	ReportSHA256             string                            `json:"report_sha256"`
	HumanReviewed            bool                              `json:"human_reviewed"`
	TechnicalGatesPassed     bool                              `json:"technical_gates_passed"`
	NetBenefitPassed         bool                              `json:"net_benefit_passed"`
	PromotionEligible        bool                              `json:"promotion_eligible"`
	RecommendedDefaultWeight int                               `json:"recommended_default_weight"`
	GateFailures             []string                          `json:"gate_failures,omitempty"`
	Metrics                  evaldomain.ParentContextABMetrics `json:"metrics"`
	Runtime                  evaldomain.ParentContextRuntime   `json:"runtime"`
	Limitations              []string                          `json:"limitations"`
}

func NewParentContextHandler(store ParentContextReportStore) *ParentContextHandler {
	return &ParentContextHandler{store: store}
}

func NewDefaultParentContextHandler() *ParentContextHandler {
	return NewParentContextHandler(NewFileParentContextReportStore(defaultParentContextReportPath))
}

func (handler *ParentContextHandler) Latest(context *gin.Context) {
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
	context.JSON(http.StatusOK, ParentContextSummaryResponse{
		SchemaVersion: parentContextResponseSchemaVersion, EvaluatorVersion: report.EvaluatorVersion,
		DatasetVersion: report.DatasetVersion, CandidateVersion: report.CandidateVersion, GeneratedAt: report.GeneratedAt,
		ReportSHA256: reportSHA, HumanReviewed: report.HumanReviewed, TechnicalGatesPassed: report.TechnicalGatesPassed,
		NetBenefitPassed: report.NetBenefitPassed, PromotionEligible: report.PromotionEligible,
		RecommendedDefaultWeight: report.RecommendedDefaultWeight, GateFailures: report.GateFailures,
		Metrics: report.Metrics, Runtime: report.Runtime,
		Limitations: []string{
			"20 条标签仍待用户人工复核，当前报告不能冻结为正式基线。",
			"答案模型与 Embedding 服务是外部可变依赖，必须保留逐例结果、版本和输入文件 Hash。",
			"净收益门只产生推荐；完成密封留出集和人工批准前，rag_parent_context 默认权重固定为 0。",
		},
	})
}

func validateParentContextReport(report evaldomain.ParentContextABReport) error {
	if report.SchemaVersion == "" || report.EvaluatorVersion != evaldomain.ParentContextEvaluatorVersion || report.DatasetVersion != evaldomain.ParentContextDatasetVersion || report.GeneratedAt.IsZero() {
		return fmt.Errorf("parent-context evaluation report metadata is inconsistent")
	}
	if report.Metrics.CaseCount != evaldomain.ParentContextCaseCount || report.Metrics.CaseCount != len(report.Cases) ||
		report.Metrics.TargetCaseCount != evaldomain.ParentContextTargetCaseCount || report.Metrics.GuardCaseCount != evaldomain.ParentContextGuardCaseCount {
		return fmt.Errorf("parent-context evaluation case counts are inconsistent")
	}
	if report.PromotionEligible && (!report.HumanReviewed || !report.TechnicalGatesPassed || !report.NetBenefitPassed) {
		return fmt.Errorf("parent-context promotion eligibility is inconsistent")
	}
	if report.RecommendedDefaultWeight != 0 {
		return fmt.Errorf("candidate report cannot enable default traffic")
	}
	if report.TechnicalGatesPassed && len(report.GateFailures) > 0 {
		return fmt.Errorf("parent-context technical gate status is inconsistent")
	}
	return nil
}

func (handler *ParentContextHandler) writeError(context *gin.Context) {
	_, traceID := requestid.IDs(context)
	context.JSON(http.StatusServiceUnavailable, ErrorResponse{
		SchemaVersion: parentContextResponseSchemaVersion, Code: "PARENT_CONTEXT_EVALUATION_UNAVAILABLE",
		Message: "父子上下文成对评测报告暂时不可用", Retryable: true, TraceID: traceID,
	})
}
