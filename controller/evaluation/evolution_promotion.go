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
	"GopherAI/internal/evolution"

	"github.com/gin-gonic/gin"
)

const maximumPromotionReviewBodyBytes = 8 * 1024

type EvolutionPromotionService interface {
	Audit(context.Context, string) (evolution.PromotionAudit, error)
	Review(context.Context, string, evolution.PromotionReviewCommand) (evolution.PromotionReviewReceipt, error)
}

type EvolutionPromotionHandler struct{ service EvolutionPromotionService }

type evolutionPromotionReviewRequest struct {
	Mode              string `json:"mode"`
	ExperimentVersion string `json:"experiment_version"`
	CandidateSHA256   string `json:"candidate_sha256"`
	ReportSHA256      string `json:"report_sha256"`
	Decision          string `json:"decision"`
	ReasonCode        string `json:"reason_code"`
	IdempotencyKey    string `json:"idempotency_key"`
	Acknowledgment    string `json:"acknowledgment"`
}

func NewEvolutionPromotionHandler(service EvolutionPromotionService) *EvolutionPromotionHandler {
	return &EvolutionPromotionHandler{service: service}
}

func NewDefaultEvolutionPromotionHandler() *EvolutionPromotionHandler {
	service, err := evolution.NewPromotionService(
		evolution.NewFileComparisonStore(defaultEvolutionComparisonPath),
		evolution.NewGormPromotionRepository(mysql.DB),
		evolution.NewGormPromotionAuthorizer(mysql.DB),
		time.Now,
	)
	if err != nil {
		return &EvolutionPromotionHandler{}
	}
	return NewEvolutionPromotionHandler(service)
}

func (handler *EvolutionPromotionHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeEvolutionError(ginContext, http.StatusServiceUnavailable, "HARNESS_PROMOTION_UNAVAILABLE", "Harness 人工晋级门暂不可用", true)
		return
	}
	principal := strings.TrimSpace(ginContext.GetString("userName"))
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 4*time.Second)
	defer cancel()
	audit, err := handler.service.Audit(ctx, principal)
	if err != nil {
		writeEvolutionError(ginContext, http.StatusServiceUnavailable, "HARNESS_PROMOTION_AUDIT_FAILED", "Harness 人工晋级审计读取失败", true)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, audit)
}

func (handler *EvolutionPromotionHandler) Review(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeEvolutionError(ginContext, http.StatusServiceUnavailable, "HARNESS_PROMOTION_UNAVAILABLE", "Harness 人工晋级门暂不可用", true)
		return
	}
	ginContext.Request.Body = http.MaxBytesReader(ginContext.Writer, ginContext.Request.Body, maximumPromotionReviewBodyBytes)
	decoder := json.NewDecoder(ginContext.Request.Body)
	decoder.DisallowUnknownFields()
	var request evolutionPromotionReviewRequest
	if err := decoder.Decode(&request); err != nil || request.Mode != evolution.PromotionMode {
		writeEvolutionError(ginContext, http.StatusBadRequest, "HARNESS_PROMOTION_REQUEST_INVALID", "人工晋级请求格式无效", false)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeEvolutionError(ginContext, http.StatusBadRequest, "HARNESS_PROMOTION_REQUEST_INVALID", "人工晋级请求包含多余内容", false)
		return
	}
	command := evolution.PromotionReviewCommand{
		ExperimentVersion: strings.TrimSpace(request.ExperimentVersion), CandidateSHA256: strings.TrimSpace(request.CandidateSHA256),
		ReportSHA256: strings.TrimSpace(request.ReportSHA256), Decision: strings.TrimSpace(request.Decision), ReasonCode: strings.TrimSpace(request.ReasonCode),
		IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), Acknowledgment: request.Acknowledgment,
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 5*time.Second)
	defer cancel()
	receipt, err := handler.service.Review(ctx, ginContext.GetString("userName"), command)
	if err != nil {
		switch {
		case errors.Is(err, evolution.ErrPromotionPermissionDenied):
			writeEvolutionError(ginContext, http.StatusForbidden, "HARNESS_PROMOTION_PERMISSION_DENIED", "当前账号没有 Harness 人工晋级权限", false)
		case errors.Is(err, evolution.ErrPromotionRequestInvalid):
			writeEvolutionError(ginContext, http.StatusBadRequest, "HARNESS_PROMOTION_REQUEST_INVALID", "人工晋级请求未通过严格校验", false)
		case errors.Is(err, evolution.ErrPromotionReportStale):
			writeEvolutionError(ginContext, http.StatusConflict, "HARNESS_PROMOTION_REPORT_STALE", "页面报告已过期，请刷新后重新评审", false)
		case errors.Is(err, evolution.ErrPromotionIdempotency):
			writeEvolutionError(ginContext, http.StatusConflict, "HARNESS_PROMOTION_IDEMPOTENCY_CONFLICT", "幂等键已绑定另一份评审内容", false)
		default:
			writeEvolutionError(ginContext, http.StatusUnprocessableEntity, "HARNESS_PROMOTION_REVIEW_FAILED", "人工晋级评审无法记录", false)
		}
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, receipt)
}
