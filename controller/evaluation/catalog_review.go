package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"GopherAI/common/mysql"
	"GopherAI/internal/catalogreview"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const (
	catalogReviewMode         = "human_catalog_case_review"
	maxCatalogReviewBodyBytes = 16 << 10
)

type CatalogReviewService interface {
	List(context.Context, string, catalogreview.Query) (catalogreview.Workbench, error)
	Submit(context.Context, string, catalogreview.ReviewCommand) (catalogreview.Receipt, error)
}

type CatalogReviewHandler struct{ service CatalogReviewService }

func NewCatalogReviewHandler(service CatalogReviewService) *CatalogReviewHandler {
	return &CatalogReviewHandler{service: service}
}

func NewDefaultCatalogReviewHandler() *CatalogReviewHandler {
	return NewCatalogReviewHandler(catalogreview.NewService(
		catalogreview.NewFileArtifactStore(catalogreview.DefaultManifestPath),
		catalogreview.NewGormRepository(mysql.DB), time.Now,
	))
}

func (handler *CatalogReviewHandler) List(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeCatalogReviewError(ginContext, http.StatusServiceUnavailable, "CATALOG_REVIEW_UNAVAILABLE", "逐例复核工作台暂不可用", true)
		return
	}
	page, err := positiveQueryInt(ginContext.Query("page"), 1)
	if err != nil {
		writeCatalogReviewError(ginContext, http.StatusBadRequest, "INVALID_CATALOG_REVIEW_QUERY", "复核分页参数无效", false)
		return
	}
	pageSize, err := positiveQueryInt(ginContext.Query("page_size"), 1)
	if err != nil {
		writeCatalogReviewError(ginContext, http.StatusBadRequest, "INVALID_CATALOG_REVIEW_QUERY", "复核分页参数无效", false)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 5*time.Second)
	defer cancel()
	workbench, err := handler.service.List(ctx, ginContext.GetString("userName"), catalogreview.Query{
		Slice: ginContext.Query("slice"), Status: ginContext.Query("status"), Page: page, PageSize: pageSize,
	})
	if err != nil {
		if errors.Is(err, catalogreview.ErrInvalidQuery) {
			writeCatalogReviewError(ginContext, http.StatusBadRequest, "INVALID_CATALOG_REVIEW_QUERY", "复核筛选或分页参数无效", false)
			return
		}
		writeCatalogReviewError(ginContext, http.StatusServiceUnavailable, "CATALOG_REVIEW_UNAVAILABLE", "逐例复核工作台暂不可用", true)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, workbench)
}

type catalogReviewRequest struct {
	Mode             string   `json:"mode"`
	CatalogSHA256    string   `json:"catalog_sha256"`
	CaseID           string   `json:"case_id"`
	CaseSHA256       string   `json:"case_sha256"`
	ExpectedRevision int      `json:"expected_revision"`
	Decision         string   `json:"decision"`
	ReasonCodes      []string `json:"reason_codes"`
	IdempotencyKey   string   `json:"idempotency_key"`
	Acknowledgment   string   `json:"acknowledgment"`
}

func (handler *CatalogReviewHandler) Submit(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeCatalogReviewError(ginContext, http.StatusServiceUnavailable, "CATALOG_REVIEW_UNAVAILABLE", "逐例复核工作台暂不可用", true)
		return
	}
	ginContext.Request.Body = http.MaxBytesReader(ginContext.Writer, ginContext.Request.Body, maxCatalogReviewBodyBytes)
	decoder := json.NewDecoder(ginContext.Request.Body)
	decoder.DisallowUnknownFields()
	request := catalogReviewRequest{}
	if err := decoder.Decode(&request); err != nil || request.Mode != catalogReviewMode {
		writeCatalogReviewError(ginContext, http.StatusBadRequest, "INVALID_CATALOG_REVIEW", "逐例复核请求格式无效", false)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeCatalogReviewError(ginContext, http.StatusBadRequest, "INVALID_CATALOG_REVIEW", "逐例复核请求包含多余内容", false)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 5*time.Second)
	defer cancel()
	receipt, err := handler.service.Submit(ctx, ginContext.GetString("userName"), catalogreview.ReviewCommand{
		CatalogSHA256: request.CatalogSHA256, CaseID: request.CaseID, CaseSHA256: request.CaseSHA256,
		ExpectedRevision: request.ExpectedRevision, Decision: request.Decision, ReasonCodes: request.ReasonCodes,
		IdempotencyKey: request.IdempotencyKey, Acknowledgment: request.Acknowledgment,
	})
	if err != nil {
		switch {
		case errors.Is(err, catalogreview.ErrInvalidReview):
			writeCatalogReviewError(ginContext, http.StatusBadRequest, "INVALID_CATALOG_REVIEW", "请选择有效结论、原因并确认已核对输入与期望结果", false)
		case errors.Is(err, catalogreview.ErrCaseNotFound):
			writeCatalogReviewError(ginContext, http.StatusNotFound, "CATALOG_REVIEW_CASE_NOT_FOUND", "评测用例不存在", false)
		case errors.Is(err, catalogreview.ErrRevisionConflict):
			writeCatalogReviewError(ginContext, http.StatusConflict, "CATALOG_REVIEW_STALE", "数据集或复核版本已变化，请刷新后重试", false)
		case errors.Is(err, catalogreview.ErrIdempotencyConflict):
			writeCatalogReviewError(ginContext, http.StatusConflict, "CATALOG_REVIEW_IDEMPOTENCY_CONFLICT", "幂等键已绑定另一项复核", false)
		default:
			writeCatalogReviewError(ginContext, http.StatusServiceUnavailable, "CATALOG_REVIEW_PERSIST_FAILED", "逐例复核暂未可靠写入", true)
		}
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, receipt)
}

func positiveQueryInt(value string, fallback int) (int, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return 0, catalogreview.ErrInvalidQuery
	}
	return parsed, nil
}

func writeCatalogReviewError(ginContext *gin.Context, status int, code, message string, retryable bool) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(status, ErrorResponse{SchemaVersion: catalogreview.SchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}
