package evaluation

import (
	"net/http"
	"strings"

	evaldomain "GopherAI/internal/evaluation"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const (
	catalogResponseSchemaVersion = "evaluation-catalog-summary-v1"
	defaultEvalCatalogPath       = "evals/devsupport-eval-v1.manifest.json"
	defaultReviewManifestPath    = "evals/devsupport-eval-v1.review.json"
)

type CatalogHandler struct {
	manifestPath string
	reviewPath   string
}

type CatalogSummaryResponse struct {
	SchemaVersion    string                                    `json:"schema_version"`
	DatasetVersion   string                                    `json:"dataset_version"`
	ManifestSHA256   string                                    `json:"manifest_sha256"`
	ExpectedTotal    int                                       `json:"expected_total"`
	ActualTotal      int                                       `json:"actual_total"`
	UniqueIDs        int                                       `json:"unique_ids"`
	SensitiveHits    int                                       `json:"sensitive_hits"`
	SchemaPassed     bool                                      `json:"schema_passed"`
	HumanReviewed    bool                                      `json:"human_reviewed"`
	BaselineEligible bool                                      `json:"baseline_eligible"`
	ReviewManifest   evaldomain.ReviewManifestValidationReport `json:"review_manifest"`
	Slices           []evaldomain.EvalCatalogSliceResult       `json:"slices"`
	Errors           []string                                  `json:"errors,omitempty"`
	Limitations      []string                                  `json:"limitations"`
}

func NewCatalogHandler(manifestPath string, reviewPaths ...string) *CatalogHandler {
	reviewPath := ""
	if len(reviewPaths) > 0 {
		reviewPath = reviewPaths[0]
	} else if strings.TrimSpace(manifestPath) != "" {
		reviewPath = strings.TrimSuffix(manifestPath, ".manifest.json") + ".review.json"
	}
	return &CatalogHandler{manifestPath: manifestPath, reviewPath: reviewPath}
}

func NewDefaultCatalogHandler() *CatalogHandler {
	return NewCatalogHandler(defaultEvalCatalogPath, defaultReviewManifestPath)
}

func (handler *CatalogHandler) Latest(context *gin.Context) {
	if handler == nil || handler.manifestPath == "" {
		handler.writeError(context)
		return
	}
	report, err := evaldomain.ValidateEvalCatalogFile(handler.manifestPath)
	if err != nil {
		handler.writeError(context)
		return
	}
	reviewReport, reviewErr := evaldomain.ValidateReviewManifestFile(handler.reviewPath, handler.manifestPath)
	if reviewErr != nil {
		reviewReport = evaldomain.ReviewManifestValidationReport{
			SchemaVersion: evaldomain.ReviewManifestSchemaVersion, Passed: false, BaselineEligible: false,
			Errors:     []string{"review manifest is unavailable or invalid"},
			Guardrails: []string{"pending_user_blocks_baseline"},
		}
	}
	humanReviewed := report.Passed
	for _, slice := range report.Slices {
		if slice.ReviewCounts["pending_user"] != 0 || slice.ReviewCounts["human"] != slice.ActualCount {
			humanReviewed = false
			break
		}
	}
	status := http.StatusOK
	if !report.Passed {
		status = http.StatusUnprocessableEntity
	}
	context.JSON(status, CatalogSummaryResponse{
		SchemaVersion: catalogResponseSchemaVersion, DatasetVersion: report.DatasetVersion,
		ManifestSHA256: report.ManifestSHA256, ExpectedTotal: report.ExpectedTotal, ActualTotal: report.ActualTotal,
		UniqueIDs: report.UniqueIDs, SensitiveHits: report.SensitiveHits, SchemaPassed: report.Passed,
		HumanReviewed:    humanReviewed && reviewReport.HumanReviewed,
		BaselineEligible: report.Passed && humanReviewed && reviewReport.BaselineEligible,
		ReviewManifest:   reviewReport,
		Slices:           report.Slices, Errors: report.Errors,
		Limitations: []string{
			"目录通过只说明数量、版本、Hash、唯一 ID、复核状态和凭据特征检查，不代表模型质量已经达标。",
			"所有 pending_user 标签都必须由用户复核后才能冻结 Full 320 基线。",
			"独立 Review Manifest 必须同时绑定 Catalog、Slice 与 Fixture Hash；切片级审批不能替代逐例 human 标签。",
		},
	})
}

func (handler *CatalogHandler) writeError(context *gin.Context) {
	_, traceID := requestid.IDs(context)
	context.JSON(http.StatusServiceUnavailable, ErrorResponse{
		SchemaVersion: catalogResponseSchemaVersion, Code: "EVALUATION_CATALOG_UNAVAILABLE",
		Message: "评测数据目录暂时不可用", Retryable: true, TraceID: traceID,
	})
}
