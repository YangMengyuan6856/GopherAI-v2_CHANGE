package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"GopherAI/common/mysql"
	"GopherAI/internal/catalogreview"
	"GopherAI/internal/catalogseal"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const (
	catalogSealMode         = "materialize_reviewed_catalog_candidate"
	maxCatalogSealBodyBytes = 16 << 10
)

type CatalogSealService interface {
	Status(context.Context, string) (catalogseal.Status, error)
	Seal(context.Context, string, catalogseal.Command) (catalogseal.Receipt, error)
}

type CatalogSealHandler struct{ service CatalogSealService }

func NewCatalogSealHandler(service CatalogSealService) *CatalogSealHandler {
	return &CatalogSealHandler{service: service}
}

func NewDefaultCatalogSealHandler() *CatalogSealHandler {
	reviews := catalogreview.NewService(
		catalogreview.NewFileArtifactStore(catalogreview.DefaultManifestPath),
		catalogreview.NewGormRepository(mysql.DB), time.Now,
	)
	return NewCatalogSealHandler(catalogseal.NewService(
		reviews, catalogseal.DefaultCatalogPath, catalogseal.DefaultReviewManifestPath, catalogseal.DefaultOutputRoot, time.Now,
	))
}

func (handler *CatalogSealHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeCatalogSealError(ginContext, http.StatusServiceUnavailable, "CATALOG_SEAL_UNAVAILABLE", "Full 320 封存服务暂不可用", true)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 5*time.Second)
	defer cancel()
	status, err := handler.service.Status(ctx, ginContext.GetString("userName"))
	if err != nil {
		writeCatalogSealError(ginContext, http.StatusServiceUnavailable, "CATALOG_SEAL_UNAVAILABLE", "Full 320 封存状态暂不可用", true)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, status)
}

type catalogSealRequest struct {
	Mode            string `json:"mode"`
	CatalogSHA256   string `json:"catalog_sha256"`
	ReviewSetSHA256 string `json:"review_set_sha256"`
	Acknowledgment  string `json:"acknowledgment"`
}

func (handler *CatalogSealHandler) Seal(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeCatalogSealError(ginContext, http.StatusServiceUnavailable, "CATALOG_SEAL_UNAVAILABLE", "Full 320 封存服务暂不可用", true)
		return
	}
	ginContext.Request.Body = http.MaxBytesReader(ginContext.Writer, ginContext.Request.Body, maxCatalogSealBodyBytes)
	decoder := json.NewDecoder(ginContext.Request.Body)
	decoder.DisallowUnknownFields()
	var request catalogSealRequest
	if err := decoder.Decode(&request); err != nil || request.Mode != catalogSealMode {
		writeCatalogSealError(ginContext, http.StatusBadRequest, "INVALID_CATALOG_SEAL_REQUEST", "Full 320 封存请求格式无效", false)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeCatalogSealError(ginContext, http.StatusBadRequest, "INVALID_CATALOG_SEAL_REQUEST", "Full 320 封存请求包含多余内容", false)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 30*time.Second)
	defer cancel()
	receipt, err := handler.service.Seal(ctx, ginContext.GetString("userName"), catalogseal.Command{
		CatalogSHA256: strings.TrimSpace(request.CatalogSHA256), ReviewSetSHA256: strings.TrimSpace(request.ReviewSetSHA256), Acknowledgment: request.Acknowledgment,
	})
	if err != nil {
		switch {
		case errors.Is(err, catalogseal.ErrInvalidCommand):
			writeCatalogSealError(ginContext, http.StatusBadRequest, "INVALID_CATALOG_SEAL_REQUEST", "请逐例完成复核并显式确认封存边界", false)
		case errors.Is(err, catalogseal.ErrReviewIncomplete):
			writeCatalogSealError(ginContext, http.StatusConflict, "CATALOG_REVIEW_INCOMPLETE", "只有 320 条全部人工通过且无退回项后才能封存", false)
		case errors.Is(err, catalogseal.ErrStaleReview):
			writeCatalogSealError(ginContext, http.StatusConflict, "CATALOG_REVIEW_STALE", "复核集合已变化，请刷新后重新确认封存", false)
		case errors.Is(err, catalogseal.ErrInvalidArtifact):
			writeCatalogSealError(ginContext, http.StatusUnprocessableEntity, "CATALOG_SEAL_INVALID", "封存候选未通过 Hash 或 Catalog 复验", false)
		default:
			writeCatalogSealError(ginContext, http.StatusServiceUnavailable, "CATALOG_SEAL_UNAVAILABLE", "Full 320 封存候选暂无法生成", true)
		}
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, receipt)
}

func writeCatalogSealError(ginContext *gin.Context, status int, code, message string, retryable bool) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(status, ErrorResponse{SchemaVersion: catalogseal.StatusSchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}
