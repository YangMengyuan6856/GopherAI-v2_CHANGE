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

const maximumShadowControlBodyBytes = 8 * 1024

type EvolutionShadowControlService interface {
	Audit(context.Context, string) (evolution.ShadowControlAudit, error)
	RequestShadow(context.Context, string, evolution.ShadowControlCommand) (evolution.ShadowControlReceipt, error)
	Rollback(context.Context, string, evolution.ShadowControlCommand) (evolution.ShadowControlReceipt, error)
}

type EvolutionShadowControlHandler struct{ service EvolutionShadowControlService }

type shadowControlRequest struct {
	Mode                 string `json:"mode"`
	ExperimentVersion    string `json:"experiment_version,omitempty"`
	ArtifactType         string `json:"artifact_type"`
	CandidateVersion     string `json:"candidate_version,omitempty"`
	CandidateSHA256      string `json:"candidate_sha256,omitempty"`
	ReportSHA256         string `json:"report_sha256,omitempty"`
	ExpectedStateVersion uint64 `json:"expected_state_version"`
	IdempotencyKey       string `json:"idempotency_key"`
	Acknowledgment       string `json:"acknowledgment"`
}

func NewEvolutionShadowControlHandler(service EvolutionShadowControlService) *EvolutionShadowControlHandler {
	return &EvolutionShadowControlHandler{service: service}
}

func NewDefaultEvolutionShadowControlHandler() *EvolutionShadowControlHandler {
	service, err := evolution.NewShadowControlService(
		evolution.NewFileComparisonStore(defaultEvolutionComparisonPath),
		evolution.NewGormShadowControlRepository(mysql.DB),
		evolution.NewGormPromotionAuthorizer(mysql.DB),
		evolution.DeterministicShadowEvaluator{},
		time.Now,
	)
	if err != nil {
		return &EvolutionShadowControlHandler{}
	}
	return NewEvolutionShadowControlHandler(service)
}

func (handler *EvolutionShadowControlHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeEvolutionError(ginContext, http.StatusServiceUnavailable, "HARNESS_SHADOW_CONTROL_UNAVAILABLE", "Harness 隔离 Shadow 控制面暂不可用", true)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 4*time.Second)
	defer cancel()
	audit, err := handler.service.Audit(ctx, ginContext.GetString("userName"))
	if err != nil {
		writeEvolutionError(ginContext, http.StatusServiceUnavailable, "HARNESS_SHADOW_CONTROL_AUDIT_FAILED", "Harness 隔离 Shadow 审计读取失败", true)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, audit)
}

func (handler *EvolutionShadowControlHandler) RequestShadow(ginContext *gin.Context) {
	handler.execute(ginContext, evolution.ControlOperationShadow)
}

func (handler *EvolutionShadowControlHandler) Rollback(ginContext *gin.Context) {
	handler.execute(ginContext, evolution.ControlOperationRollback)
}

func (handler *EvolutionShadowControlHandler) execute(ginContext *gin.Context, operation string) {
	if handler == nil || handler.service == nil {
		writeEvolutionError(ginContext, http.StatusServiceUnavailable, "HARNESS_SHADOW_CONTROL_UNAVAILABLE", "Harness 隔离 Shadow 控制面暂不可用", true)
		return
	}
	ginContext.Request.Body = http.MaxBytesReader(ginContext.Writer, ginContext.Request.Body, maximumShadowControlBodyBytes)
	decoder := json.NewDecoder(ginContext.Request.Body)
	decoder.DisallowUnknownFields()
	var request shadowControlRequest
	if err := decoder.Decode(&request); err != nil || request.Mode != evolution.ShadowControlMode {
		writeEvolutionError(ginContext, http.StatusBadRequest, "HARNESS_SHADOW_CONTROL_REQUEST_INVALID", "Harness 隔离 Shadow 控制请求格式无效", false)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeEvolutionError(ginContext, http.StatusBadRequest, "HARNESS_SHADOW_CONTROL_REQUEST_INVALID", "Harness 隔离 Shadow 控制请求包含多余内容", false)
		return
	}
	command := evolution.ShadowControlCommand{Operation: operation, ExperimentVersion: strings.TrimSpace(request.ExperimentVersion), ArtifactType: strings.TrimSpace(request.ArtifactType), CandidateVersion: strings.TrimSpace(request.CandidateVersion), CandidateSHA256: strings.TrimSpace(request.CandidateSHA256), ReportSHA256: strings.TrimSpace(request.ReportSHA256), ExpectedStateVersion: request.ExpectedStateVersion, IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), Acknowledgment: request.Acknowledgment}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 6*time.Second)
	defer cancel()
	var receipt evolution.ShadowControlReceipt
	var err error
	if operation == evolution.ControlOperationShadow {
		receipt, err = handler.service.RequestShadow(ctx, ginContext.GetString("userName"), command)
	} else {
		receipt, err = handler.service.Rollback(ctx, ginContext.GetString("userName"), command)
	}
	if err != nil {
		switch {
		case errors.Is(err, evolution.ErrPromotionPermissionDenied):
			writeEvolutionError(ginContext, http.StatusForbidden, "HARNESS_SHADOW_CONTROL_PERMISSION_DENIED", "当前账号没有 Harness 隔离 Shadow 控制权限", false)
		case errors.Is(err, evolution.ErrShadowControlInvalid):
			writeEvolutionError(ginContext, http.StatusBadRequest, "HARNESS_SHADOW_CONTROL_REQUEST_INVALID", "Harness 隔离 Shadow 控制请求未通过严格校验", false)
		case errors.Is(err, evolution.ErrShadowControlStale):
			writeEvolutionError(ginContext, http.StatusConflict, "HARNESS_SHADOW_CONTROL_REPORT_STALE", "页面候选或报告已过期，请刷新后重试", false)
		case errors.Is(err, evolution.ErrShadowControlIdempotent):
			writeEvolutionError(ginContext, http.StatusConflict, "HARNESS_SHADOW_CONTROL_IDEMPOTENCY_CONFLICT", "幂等键已绑定另一项控制请求", false)
		default:
			writeEvolutionError(ginContext, http.StatusUnprocessableEntity, "HARNESS_SHADOW_CONTROL_FAILED", "Harness 隔离 Shadow 控制操作无法完成", false)
		}
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, receipt)
}
