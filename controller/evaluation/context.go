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
	contextResponseSchemaVersion = "context-evaluation-summary-v1"
	defaultContextReportPath     = "evals/results/devsupport-context-compression-v1-candidate.json"
)

type ContextReportStore interface {
	Load(context.Context) (evaldomain.ContextEvaluationReport, string, error)
}

type FileContextReportStore struct{ path string }

func NewFileContextReportStore(path string) *FileContextReportStore {
	return &FileContextReportStore{path: strings.TrimSpace(path)}
}

func (store *FileContextReportStore) Load(_ context.Context) (evaldomain.ContextEvaluationReport, string, error) {
	if store == nil || store.path == "" {
		return evaldomain.ContextEvaluationReport{}, "", fmt.Errorf("context evaluation report path is required")
	}
	file, err := os.Open(store.path)
	if err != nil {
		return evaldomain.ContextEvaluationReport{}, "", err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxMemoryReportBytes+1))
	if err != nil || len(encoded) == 0 || len(encoded) > maxMemoryReportBytes {
		return evaldomain.ContextEvaluationReport{}, "", fmt.Errorf("context evaluation report is unavailable")
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var report evaldomain.ContextEvaluationReport
	if err := decoder.Decode(&report); err != nil {
		return evaldomain.ContextEvaluationReport{}, "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return evaldomain.ContextEvaluationReport{}, "", fmt.Errorf("context evaluation report has trailing content")
	}
	if err := validateContextReport(report); err != nil {
		return evaldomain.ContextEvaluationReport{}, "", err
	}
	digest := sha256.Sum256(encoded)
	return report, hex.EncodeToString(digest[:]), nil
}

type ContextHandler struct{ store ContextReportStore }

type ContextSummaryResponse struct {
	SchemaVersion        string                              `json:"schema_version"`
	EvaluatorVersion     string                              `json:"evaluator_version"`
	DatasetVersion       string                              `json:"dataset_version"`
	GeneratedAt          time.Time                           `json:"generated_at"`
	ReportSHA256         string                              `json:"report_sha256"`
	HumanReviewed        bool                                `json:"human_reviewed"`
	BaselineEligible     bool                                `json:"baseline_eligible"`
	TechnicalGatesPassed bool                                `json:"technical_gates_passed"`
	GateFailures         []string                            `json:"gate_failures,omitempty"`
	Metrics              evaldomain.ContextEvaluationMetrics `json:"metrics"`
	Limitations          []string                            `json:"limitations"`
}

func NewContextHandler(store ContextReportStore) *ContextHandler {
	return &ContextHandler{store: store}
}

func NewDefaultContextHandler() *ContextHandler {
	return NewContextHandler(NewFileContextReportStore(defaultContextReportPath))
}

func (handler *ContextHandler) LatestContext(context *gin.Context) {
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
	context.JSON(http.StatusOK, ContextSummaryResponse{
		SchemaVersion: contextResponseSchemaVersion, EvaluatorVersion: report.EvaluatorVersion,
		DatasetVersion: report.DatasetVersion, GeneratedAt: report.GeneratedAt, ReportSHA256: reportSHA,
		HumanReviewed: report.HumanReviewed, BaselineEligible: report.BaselineEligible,
		TechnicalGatesPassed: report.TechnicalGatesPassed, GateFailures: report.GateFailures, Metrics: report.Metrics,
		Limitations: []string{
			"12 条标签仍待用户人工复核，因此当前只是一份技术候选报告。",
			"Token 为本地稳定估算，不是模型供应商账单 Token。",
			"当前覆盖确定性 Checkpoint 压缩，不代表任意开放域长对话摘要质量。",
		},
	})
}

func validateContextReport(report evaldomain.ContextEvaluationReport) error {
	if report.EvaluatorVersion == "" || report.DatasetVersion == "" || report.GeneratedAt.IsZero() || report.Metrics.CaseCount != len(report.Cases) || report.Metrics.CaseCount != 12 {
		return fmt.Errorf("context evaluation report metadata is inconsistent")
	}
	if report.BaselineEligible && !report.HumanReviewed {
		return fmt.Errorf("unreviewed context evaluation cannot be baseline eligible")
	}
	if report.TechnicalGatesPassed && len(report.GateFailures) > 0 {
		return fmt.Errorf("context evaluation gate status is inconsistent")
	}
	return nil
}

func (handler *ContextHandler) writeError(context *gin.Context) {
	_, traceID := requestid.IDs(context)
	context.JSON(http.StatusServiceUnavailable, ErrorResponse{
		SchemaVersion: contextResponseSchemaVersion, Code: "CONTEXT_EVALUATION_UNAVAILABLE",
		Message: "上下文压缩评测报告暂时不可用", Retryable: true, TraceID: traceID,
	})
}
