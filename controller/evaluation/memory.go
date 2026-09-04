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
	memoryResponseSchemaVersion = "memory-evaluation-summary-v1"
	defaultMemoryReportPath     = "evals/results/devsupport-memory-v1-candidate.json"
	maxMemoryReportBytes        = 8 << 20
)

type MemoryReportStore interface {
	Load(context.Context) (evaldomain.MemoryEvaluationReport, string, error)
}

type FileMemoryReportStore struct{ path string }

func NewFileMemoryReportStore(path string) *FileMemoryReportStore {
	return &FileMemoryReportStore{path: strings.TrimSpace(path)}
}

func (store *FileMemoryReportStore) Load(_ context.Context) (evaldomain.MemoryEvaluationReport, string, error) {
	if store == nil || store.path == "" {
		return evaldomain.MemoryEvaluationReport{}, "", fmt.Errorf("memory evaluation report path is required")
	}
	file, err := os.Open(store.path)
	if err != nil {
		return evaldomain.MemoryEvaluationReport{}, "", err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxMemoryReportBytes+1))
	if err != nil || len(encoded) == 0 || len(encoded) > maxMemoryReportBytes {
		return evaldomain.MemoryEvaluationReport{}, "", fmt.Errorf("memory evaluation report is unavailable")
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var report evaldomain.MemoryEvaluationReport
	if err := decoder.Decode(&report); err != nil {
		return evaldomain.MemoryEvaluationReport{}, "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return evaldomain.MemoryEvaluationReport{}, "", fmt.Errorf("memory evaluation report has trailing content")
	}
	if err := validateMemoryReport(report); err != nil {
		return evaldomain.MemoryEvaluationReport{}, "", err
	}
	digest := sha256.Sum256(encoded)
	return report, hex.EncodeToString(digest[:]), nil
}

type MemoryHandler struct{ store MemoryReportStore }

type MemorySummaryResponse struct {
	SchemaVersion        string                             `json:"schema_version"`
	EvaluatorVersion     string                             `json:"evaluator_version"`
	DatasetVersion       string                             `json:"dataset_version"`
	GeneratedAt          time.Time                          `json:"generated_at"`
	ReportSHA256         string                             `json:"report_sha256"`
	HumanReviewed        bool                               `json:"human_reviewed"`
	BaselineEligible     bool                               `json:"baseline_eligible"`
	TechnicalGatesPassed bool                               `json:"technical_gates_passed"`
	GateFailures         []string                           `json:"gate_failures,omitempty"`
	Metrics              evaldomain.MemoryEvaluationMetrics `json:"metrics"`
	Limitations          []string                           `json:"limitations"`
}

func NewMemoryHandler(store MemoryReportStore) *MemoryHandler { return &MemoryHandler{store: store} }

func NewDefaultMemoryHandler() *MemoryHandler {
	return NewMemoryHandler(NewFileMemoryReportStore(defaultMemoryReportPath))
}

func (handler *MemoryHandler) LatestMemory(context *gin.Context) {
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
	context.JSON(http.StatusOK, MemorySummaryResponse{
		SchemaVersion: memoryResponseSchemaVersion, EvaluatorVersion: report.EvaluatorVersion,
		DatasetVersion: report.DatasetVersion, GeneratedAt: report.GeneratedAt, ReportSHA256: reportSHA,
		HumanReviewed: report.HumanReviewed, BaselineEligible: report.BaselineEligible,
		TechnicalGatesPassed: report.TechnicalGatesPassed, GateFailures: report.GateFailures, Metrics: report.Metrics,
		Limitations: []string{
			"20 条记忆标签仍待用户人工复核，因此当前只是一份技术候选报告。",
			"当前是确定性契约集，覆盖相关、过期/冲突、删除、跨用户和预算，不等同于真实数据库故障注入。",
			"报告证明召回与隔离规则，不代表长对话回答质量或向量语义召回效果。",
		},
	})
}

func validateMemoryReport(report evaldomain.MemoryEvaluationReport) error {
	if report.EvaluatorVersion == "" || report.DatasetVersion == "" || report.GeneratedAt.IsZero() || report.Metrics.CaseCount != len(report.Cases) || report.Metrics.CaseCount != 20 {
		return fmt.Errorf("memory evaluation report metadata is inconsistent")
	}
	if report.BaselineEligible && !report.HumanReviewed {
		return fmt.Errorf("unreviewed memory evaluation cannot be baseline eligible")
	}
	if report.TechnicalGatesPassed && len(report.GateFailures) > 0 {
		return fmt.Errorf("memory evaluation gate status is inconsistent")
	}
	return nil
}

func (handler *MemoryHandler) writeError(context *gin.Context) {
	_, traceID := requestid.IDs(context)
	context.JSON(http.StatusServiceUnavailable, ErrorResponse{
		SchemaVersion: memoryResponseSchemaVersion, Code: "MEMORY_EVALUATION_UNAVAILABLE",
		Message: "记忆评测报告暂时不可用", Retryable: true, TraceID: traceID,
	})
}
