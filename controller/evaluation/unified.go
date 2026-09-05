package evaluation

import (
	"bytes"
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
	unifiedResponseSchemaVersion = "unified-evaluation-summary-v1"
	defaultUnifiedReportPath     = "evals/results/devsupport-eval-run-v1-candidate.json"
	maxUnifiedReportBytes        = 4 << 20
)

type UnifiedReportStore interface {
	Load() (evaldomain.UnifiedEvaluationReport, string, error)
}

type FileUnifiedReportStore struct{ path string }

func NewFileUnifiedReportStore(path string) *FileUnifiedReportStore {
	return &FileUnifiedReportStore{path: strings.TrimSpace(path)}
}

func (store *FileUnifiedReportStore) Load() (evaldomain.UnifiedEvaluationReport, string, error) {
	if store == nil || store.path == "" {
		return evaldomain.UnifiedEvaluationReport{}, "", fmt.Errorf("unified evaluation report path is required")
	}
	encoded, err := os.ReadFile(store.path)
	if err != nil || len(encoded) == 0 || len(encoded) > maxUnifiedReportBytes {
		return evaldomain.UnifiedEvaluationReport{}, "", fmt.Errorf("unified evaluation report is unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var report evaldomain.UnifiedEvaluationReport
	if err := decoder.Decode(&report); err != nil {
		return report, "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return report, "", fmt.Errorf("unified evaluation report has trailing content")
	}
	if err := evaldomain.ValidateUnifiedEvaluationReport(report); err != nil {
		return report, "", err
	}
	digest := sha256.Sum256(encoded)
	return report, hex.EncodeToString(digest[:]), nil
}

type UnifiedHandler struct{ store UnifiedReportStore }

type FailureClusterSummary struct {
	Slice string `json:"slice"`
	Code  string `json:"code"`
	Count int    `json:"count"`
}

type UnifiedSummaryResponse struct {
	SchemaVersion    string                            `json:"schema_version"`
	RunnerVersion    string                            `json:"runner_version"`
	RunID            string                            `json:"run_id"`
	CandidateVersion string                            `json:"candidate_version"`
	GeneratedAt      time.Time                         `json:"generated_at"`
	ReportSHA256     string                            `json:"report_sha256"`
	DatasetVersion   string                            `json:"dataset_version"`
	ManifestSHA256   string                            `json:"manifest_sha256"`
	Artifacts        []evaldomain.EvaluationArtifact   `json:"artifacts"`
	Coverage         evaldomain.EvaluationCoverage     `json:"coverage"`
	Scorecard        evaldomain.DeterministicScorecard `json:"scorecard"`
	FailureClusters  []FailureClusterSummary           `json:"failure_clusters"`
	Decision         evaldomain.EvaluationDecision     `json:"decision"`
	Limitations      []string                          `json:"limitations"`
}

func NewUnifiedHandler(store UnifiedReportStore) *UnifiedHandler {
	return &UnifiedHandler{store: store}
}

func NewDefaultUnifiedHandler() *UnifiedHandler {
	return NewUnifiedHandler(NewFileUnifiedReportStore(defaultUnifiedReportPath))
}

func (handler *UnifiedHandler) Latest(context *gin.Context) {
	if handler == nil || handler.store == nil {
		handler.writeError(context)
		return
	}
	report, reportSHA, err := handler.store.Load()
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
	clusters := make([]FailureClusterSummary, 0, len(report.FailureClusters))
	for _, cluster := range report.FailureClusters {
		clusters = append(clusters, FailureClusterSummary{Slice: cluster.Slice, Code: cluster.Code, Count: cluster.Count})
	}
	context.JSON(http.StatusOK, UnifiedSummaryResponse{
		SchemaVersion: unifiedResponseSchemaVersion, RunnerVersion: report.RunnerVersion, RunID: report.RunID,
		CandidateVersion: report.CandidateVersion, GeneratedAt: report.GeneratedAt, ReportSHA256: reportSHA,
		DatasetVersion: report.DatasetVersion, ManifestSHA256: report.ManifestSHA256, Artifacts: report.Artifacts,
		Coverage: report.Coverage, Scorecard: report.Scorecard, FailureClusters: clusters, Decision: report.Decision,
		Limitations: report.Limitations,
	})
}

func (handler *UnifiedHandler) writeError(context *gin.Context) {
	_, traceID := requestid.IDs(context)
	context.JSON(http.StatusServiceUnavailable, ErrorResponse{
		SchemaVersion: unifiedResponseSchemaVersion, Code: "UNIFIED_EVALUATION_UNAVAILABLE",
		Message: "统一评测报告暂时不可用", Retryable: true, TraceID: traceID,
	})
}
