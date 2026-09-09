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
	toolResponseSchemaVersion = "tool-evaluation-summary-v1"
	defaultToolReportPath     = "evals/results/devsupport-tool-runtime-v1-candidate.json"
	maxToolReportBytes        = 8 << 20
)

type ToolReportStore interface {
	Load(context.Context) (evaldomain.ToolEvaluationReport, string, error)
}

type FileToolReportStore struct{ path string }

func NewFileToolReportStore(path string) *FileToolReportStore {
	return &FileToolReportStore{path: strings.TrimSpace(path)}
}

func (store *FileToolReportStore) Load(_ context.Context) (evaldomain.ToolEvaluationReport, string, error) {
	if store == nil || store.path == "" {
		return evaldomain.ToolEvaluationReport{}, "", fmt.Errorf("tool evaluation report path is required")
	}
	file, err := os.Open(store.path)
	if err != nil {
		return evaldomain.ToolEvaluationReport{}, "", err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxToolReportBytes+1))
	if err != nil || len(encoded) == 0 || len(encoded) > maxToolReportBytes {
		return evaldomain.ToolEvaluationReport{}, "", fmt.Errorf("tool evaluation report is unavailable")
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var report evaldomain.ToolEvaluationReport
	if err := decoder.Decode(&report); err != nil {
		return evaldomain.ToolEvaluationReport{}, "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return evaldomain.ToolEvaluationReport{}, "", fmt.Errorf("tool evaluation report has trailing content")
	}
	if err := validateToolReport(report); err != nil {
		return evaldomain.ToolEvaluationReport{}, "", err
	}
	digest := sha256.Sum256(encoded)
	return report, hex.EncodeToString(digest[:]), nil
}

type ToolHandler struct{ store ToolReportStore }

type ToolSummaryResponse struct {
	SchemaVersion        string                           `json:"schema_version"`
	EvaluatorVersion     string                           `json:"evaluator_version"`
	DatasetVersion       string                           `json:"dataset_version"`
	GeneratedAt          time.Time                        `json:"generated_at"`
	ReportSHA256         string                           `json:"report_sha256"`
	HumanReviewed        bool                             `json:"human_reviewed"`
	BaselineEligible     bool                             `json:"baseline_eligible"`
	TechnicalGatesPassed bool                             `json:"technical_gates_passed"`
	GateFailures         []string                         `json:"gate_failures,omitempty"`
	Metrics              evaldomain.ToolEvaluationMetrics `json:"metrics"`
	Limitations          []string                         `json:"limitations"`
}

func NewToolHandler(store ToolReportStore) *ToolHandler { return &ToolHandler{store: store} }

func NewDefaultToolHandler() *ToolHandler {
	return NewToolHandler(NewFileToolReportStore(defaultToolReportPath))
}

func (handler *ToolHandler) LatestTool(context *gin.Context) {
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
	context.JSON(http.StatusOK, ToolSummaryResponse{
		SchemaVersion: toolResponseSchemaVersion, EvaluatorVersion: report.EvaluatorVersion,
		DatasetVersion: report.DatasetVersion, GeneratedAt: report.GeneratedAt, ReportSHA256: reportSHA,
		HumanReviewed: report.HumanReviewed, BaselineEligible: report.BaselineEligible,
		TechnicalGatesPassed: report.TechnicalGatesPassed, GateFailures: report.GateFailures, Metrics: report.Metrics,
		Limitations: []string{
			"30 条标签仍待用户人工复核，因此当前只是一份技术候选报告。",
			"本报告复用生产候选执行器、规划器与治理运行时；工具依赖由确定性 Fixture 替代，不等同于云端网络故障演练。",
			"危险动作零执行、审计覆盖和确定性重放属于契约指标，不代表开放域 ToolAgent 已具备自主运维权限。",
		},
	})
}

func validateToolReport(report evaldomain.ToolEvaluationReport) error {
	if report.EvaluatorVersion == "" || report.DatasetVersion == "" || report.GeneratedAt.IsZero() || report.Metrics.CaseCount != len(report.Cases) || report.Metrics.CaseCount != 30 {
		return fmt.Errorf("tool evaluation report metadata is inconsistent")
	}
	if report.BaselineEligible && !report.HumanReviewed {
		return fmt.Errorf("unreviewed tool evaluation cannot be baseline eligible")
	}
	if report.TechnicalGatesPassed && len(report.GateFailures) > 0 {
		return fmt.Errorf("tool evaluation gate status is inconsistent")
	}
	if report.TechnicalGatesPassed && (report.Metrics.BoundedRepairPassRate != 1 || report.Metrics.NoProgressTerminationRate != 1) {
		return fmt.Errorf("tool evaluation candidate-governance gates are inconsistent")
	}
	return nil
}

func (handler *ToolHandler) writeError(context *gin.Context) {
	_, traceID := requestid.IDs(context)
	context.JSON(http.StatusServiceUnavailable, ErrorResponse{SchemaVersion: toolResponseSchemaVersion, Code: "TOOL_EVALUATION_UNAVAILABLE", Message: "工具评测报告暂时不可用", Retryable: true, TraceID: traceID})
}
