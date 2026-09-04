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
	responseSchemaVersion       = "diagnostic-evaluation-summary-v1"
	defaultDiagnosticReportPath = "evals/results/devsupport-diagnostic-v1-candidate.json"
	maxDiagnosticReportBytes    = 8 << 20
)

type ReportStore interface {
	Load(context.Context) (evaldomain.DiagnosticEvaluationReport, string, error)
}

type FileReportStore struct {
	path string
}

func NewFileReportStore(path string) *FileReportStore {
	return &FileReportStore{path: strings.TrimSpace(path)}
}

func (store *FileReportStore) Load(_ context.Context) (evaldomain.DiagnosticEvaluationReport, string, error) {
	if store == nil || store.path == "" {
		return evaldomain.DiagnosticEvaluationReport{}, "", fmt.Errorf("diagnostic evaluation report path is required")
	}
	file, err := os.Open(store.path)
	if err != nil {
		return evaldomain.DiagnosticEvaluationReport{}, "", err
	}
	defer file.Close()
	limited := io.LimitReader(file, maxDiagnosticReportBytes+1)
	encoded, err := io.ReadAll(limited)
	if err != nil {
		return evaldomain.DiagnosticEvaluationReport{}, "", err
	}
	if len(encoded) == 0 || len(encoded) > maxDiagnosticReportBytes {
		return evaldomain.DiagnosticEvaluationReport{}, "", fmt.Errorf("diagnostic evaluation report size is invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var report evaldomain.DiagnosticEvaluationReport
	if err := decoder.Decode(&report); err != nil {
		return evaldomain.DiagnosticEvaluationReport{}, "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return evaldomain.DiagnosticEvaluationReport{}, "", fmt.Errorf("diagnostic evaluation report has trailing content")
	}
	if err := validateReport(report); err != nil {
		return evaldomain.DiagnosticEvaluationReport{}, "", err
	}
	digest := sha256.Sum256(encoded)
	return report, hex.EncodeToString(digest[:]), nil
}

type Handler struct {
	store ReportStore
}

type SummaryResponse struct {
	SchemaVersion        string                                 `json:"schema_version"`
	EvaluatorVersion     string                                 `json:"evaluator_version"`
	DatasetVersion       string                                 `json:"dataset_version"`
	GeneratedAt          time.Time                              `json:"generated_at"`
	ReportSHA256         string                                 `json:"report_sha256"`
	HumanReviewed        bool                                   `json:"human_reviewed"`
	BaselineEligible     bool                                   `json:"baseline_eligible"`
	TechnicalGatesPassed bool                                   `json:"technical_gates_passed"`
	GateFailures         []string                               `json:"gate_failures,omitempty"`
	Metrics              evaldomain.DiagnosticEvaluationMetrics `json:"metrics"`
	Limitations          []string                               `json:"limitations"`
}

type ErrorResponse struct {
	SchemaVersion string `json:"schema_version"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Retryable     bool   `json:"retryable"`
	TraceID       string `json:"trace_id,omitempty"`
}

func NewHandler(store ReportStore) *Handler { return &Handler{store: store} }

func NewDefaultHandler() *Handler {
	return NewHandler(NewFileReportStore(defaultDiagnosticReportPath))
}

func (handler *Handler) LatestDiagnostic(context *gin.Context) {
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
	context.JSON(http.StatusOK, SummaryResponse{
		SchemaVersion: responseSchemaVersion, EvaluatorVersion: report.EvaluatorVersion,
		DatasetVersion: report.DatasetVersion, GeneratedAt: report.GeneratedAt, ReportSHA256: reportSHA,
		HumanReviewed: report.HumanReviewed, BaselineEligible: report.BaselineEligible,
		TechnicalGatesPassed: report.TechnicalGatesPassed, GateFailures: report.GateFailures, Metrics: report.Metrics,
		Limitations: []string{
			"当前 40 条标签仍待用户人工复核，因此只是一份技术候选报告，不是正式基线。",
			"规则与诊断器曾使用同一候选集迭代，尚未通过密封留出集验证泛化能力。",
			"指标用于暴露回归和安全边界，不代表线上根因确认率或自动修复成功率。",
		},
	})
}

func validateReport(report evaldomain.DiagnosticEvaluationReport) error {
	if report.EvaluatorVersion == "" || report.DatasetVersion == "" || report.GeneratedAt.IsZero() {
		return fmt.Errorf("diagnostic evaluation report metadata is incomplete")
	}
	if report.Metrics.CaseCount < 1 || report.Metrics.CaseCount != len(report.Cases) {
		return fmt.Errorf("diagnostic evaluation case count is inconsistent")
	}
	if report.BaselineEligible && !report.HumanReviewed {
		return fmt.Errorf("unreviewed diagnostic evaluation cannot be baseline eligible")
	}
	if report.TechnicalGatesPassed && len(report.GateFailures) > 0 {
		return fmt.Errorf("diagnostic evaluation gate status is inconsistent")
	}
	return nil
}

func (handler *Handler) writeError(context *gin.Context) {
	_, traceID := requestid.IDs(context)
	context.JSON(http.StatusServiceUnavailable, ErrorResponse{
		SchemaVersion: responseSchemaVersion, Code: "DIAGNOSTIC_EVALUATION_UNAVAILABLE",
		Message: "诊断评测报告暂时不可用", Retryable: true, TraceID: traceID,
	})
}
