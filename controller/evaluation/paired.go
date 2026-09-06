package evaluation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	evaldomain "GopherAI/internal/evaluation"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const pairedSummarySchemaVersion = "paired-comparison-summary-v1"

type PairedSource struct {
	Name             string    `json:"name"`
	DatasetVersion   string    `json:"dataset_version"`
	CandidateVersion string    `json:"candidate_version"`
	GeneratedAt      time.Time `json:"generated_at"`
	ReportSHA256     string    `json:"report_sha256"`
	HumanReviewed    bool      `json:"human_reviewed"`
}

type NamedPairedComparison struct {
	Name              string                      `json:"name"`
	BaselineStrategy  string                      `json:"baseline_strategy"`
	CandidateStrategy string                      `json:"candidate_strategy"`
	Population        string                      `json:"population"`
	SourceReport      string                      `json:"source_report"`
	Analysis          evaldomain.PairedComparison `json:"analysis"`
}

type PairedSummaryResponse struct {
	SchemaVersion     string                  `json:"schema_version"`
	MethodVersion     string                  `json:"method_version"`
	GeneratedAt       time.Time               `json:"generated_at"`
	AnalysisSHA256    string                  `json:"analysis_sha256"`
	HumanReviewed     bool                    `json:"human_reviewed"`
	PromotionEligible bool                    `json:"promotion_eligible"`
	Sources           []PairedSource          `json:"sources"`
	Comparisons       []NamedPairedComparison `json:"comparisons"`
	Limitations       []string                `json:"limitations"`
}

type PairedHandler struct {
	collaboration CollaborationReportStore
	parentContext ParentContextReportStore
}

func NewPairedHandler(collaboration CollaborationReportStore, parentContext ParentContextReportStore) *PairedHandler {
	return &PairedHandler{collaboration: collaboration, parentContext: parentContext}
}

func NewDefaultPairedHandler() *PairedHandler {
	return NewPairedHandler(NewFileCollaborationReportStore(defaultCollaborationReportPath), NewFileParentContextReportStore(defaultParentContextReportPath))
}

func (handler *PairedHandler) Latest(ctx *gin.Context) {
	if handler == nil || handler.collaboration == nil || handler.parentContext == nil {
		handler.writeError(ctx)
		return
	}
	collaboration, collaborationSHA, err := handler.collaboration.Load(ctx.Request.Context())
	if err != nil {
		handler.writeError(ctx)
		return
	}
	parent, parentSHA, err := handler.parentContext.Load(ctx.Request.Context())
	if err != nil {
		handler.writeError(ctx)
		return
	}
	response, err := buildPairedSummary(collaboration, collaborationSHA, parent, parentSHA)
	if err != nil {
		handler.writeError(ctx)
		return
	}
	etag := `"` + response.AnalysisSHA256 + `"`
	ctx.Header("ETag", etag)
	ctx.Header("Cache-Control", "private, max-age=30")
	if ctx.GetHeader("If-None-Match") == etag {
		ctx.Status(http.StatusNotModified)
		return
	}
	ctx.JSON(http.StatusOK, response)
}

func buildPairedSummary(collaboration evaldomain.CollaborationABReport, collaborationSHA string, parent evaldomain.ParentContextABReport, parentSHA string) (PairedSummaryResponse, error) {
	if err := validateCollaborationReport(collaboration); err != nil {
		return PairedSummaryResponse{}, err
	}
	if err := validateParentContextReport(parent); err != nil {
		return PairedSummaryResponse{}, err
	}
	collaborationObservations := make([]evaldomain.PairedObservation, 0, evaldomain.CollaborationTargetCaseCount)
	for _, item := range collaboration.Cases {
		if item.Slice == evaldomain.CollaborationSliceTarget {
			collaborationObservations = append(collaborationObservations, evaldomain.PairedObservation{BaselineScore: item.BaselineQuality, CandidateScore: item.CandidateQuality})
		}
	}
	parentObservations := make([]evaldomain.PairedObservation, 0, evaldomain.ParentContextTargetCaseCount)
	for _, item := range parent.Cases {
		if item.Slice == evaldomain.ParentContextSliceTarget {
			parentObservations = append(parentObservations, evaldomain.PairedObservation{BaselineScore: item.BaselineQuality, CandidateScore: item.CandidateQuality})
		}
	}
	collaborationAnalysis, err := evaldomain.AnalyzePairedObservations(collaborationObservations, .8)
	if err != nil {
		return PairedSummaryResponse{}, err
	}
	parentAnalysis, err := evaldomain.AnalyzePairedObservations(parentObservations, .8)
	if err != nil {
		return PairedSummaryResponse{}, err
	}
	generatedAt := collaboration.GeneratedAt.UTC()
	if parent.GeneratedAt.After(generatedAt) {
		generatedAt = parent.GeneratedAt.UTC()
	}
	response := PairedSummaryResponse{
		SchemaVersion: pairedSummarySchemaVersion, MethodVersion: evaldomain.PairedComparisonVersion, GeneratedAt: generatedAt,
		HumanReviewed: collaboration.HumanReviewed && parent.HumanReviewed, PromotionEligible: false,
		Sources: []PairedSource{
			{Name: "collaboration", DatasetVersion: collaboration.DatasetVersion, CandidateVersion: collaboration.CandidateVersion, GeneratedAt: collaboration.GeneratedAt.UTC(), ReportSHA256: collaborationSHA, HumanReviewed: collaboration.HumanReviewed},
			{Name: "parent_context", DatasetVersion: parent.DatasetVersion, CandidateVersion: parent.CandidateVersion, GeneratedAt: parent.GeneratedAt.UTC(), ReportSHA256: parentSHA, HumanReviewed: parent.HumanReviewed},
		},
		Comparisons: []NamedPairedComparison{
			{Name: "collaboration_target_quality", BaselineStrategy: "diagnosis_standard", CandidateStrategy: "diagnosis_collaborative", Population: evaldomain.CollaborationSliceTarget, SourceReport: "collaboration", Analysis: collaborationAnalysis},
			{Name: "parent_context_target_quality", BaselineStrategy: "rag_fast", CandidateStrategy: "rag_parent_context", Population: evaldomain.ParentContextSliceTarget, SourceReport: "parent_context", Analysis: parentAnalysis},
		},
		Limitations: []string{
			"比较只使用同一问题在基线与候选上的成对结果，不把两组样本误当作独立样本。",
			"95% CI 使用固定种子 2000 次 paired bootstrap；二分类成功率以质量分 >= 0.80 定义，并使用精确双侧 McNemar 检验。",
			"当前输入标签尚未完成人工复核，统计显著不等于可上线；PromotionEligible 始终保持 false。",
			"两个实验的任务、模型调用与成本口径不同，只能分别解释，禁止把样本合并为一个总体收益率。",
		},
	}
	sort.Slice(response.Sources, func(i, j int) bool { return response.Sources[i].Name < response.Sources[j].Name })
	hashInput := response
	hashInput.AnalysisSHA256 = ""
	encoded, err := json.Marshal(hashInput)
	if err != nil {
		return PairedSummaryResponse{}, err
	}
	digest := sha256.Sum256(encoded)
	response.AnalysisSHA256 = hex.EncodeToString(digest[:])
	return response, nil
}

func (handler *PairedHandler) writeError(ctx *gin.Context) {
	_, traceID := requestid.IDs(ctx)
	ctx.JSON(http.StatusServiceUnavailable, ErrorResponse{SchemaVersion: pairedSummarySchemaVersion, Code: "PAIRED_COMPARISON_UNAVAILABLE", Message: "成对统计报告暂时不可用", Retryable: true, TraceID: traceID})
}
