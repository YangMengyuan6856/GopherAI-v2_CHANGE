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
	evaldomain "GopherAI/internal/evaluation"
	"GopherAI/internal/judgecalibration"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const maxJudgeCalibrationReviewBytes = 16 << 10

type JudgeCalibrationService interface {
	Audit(context.Context, string) (judgecalibration.Audit, error)
	Submit(context.Context, string, string, evaldomain.JudgeScores) (judgecalibration.ReviewReceipt, error)
}

type JudgeCalibrationHandler struct{ service JudgeCalibrationService }

type JudgeCalibrationReviewRequest struct {
	CaseID string                 `json:"case_id" binding:"required"`
	Scores evaldomain.JudgeScores `json:"scores" binding:"required"`
}

func NewJudgeCalibrationHandler(service JudgeCalibrationService) *JudgeCalibrationHandler {
	return &JudgeCalibrationHandler{service: service}
}

func NewDefaultJudgeCalibrationHandler() *JudgeCalibrationHandler {
	return NewJudgeCalibrationHandler(judgecalibration.NewService(
		judgecalibration.NewFileArtifactStore(judgecalibration.DefaultDatasetPath, judgecalibration.DefaultReportPath),
		judgecalibration.NewGormRepository(mysql.DB), time.Now,
	))
}

func (handler *JudgeCalibrationHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeJudgeCalibrationError(ginContext, http.StatusServiceUnavailable, "JUDGE_CALIBRATION_UNAVAILABLE", "Judge 校准服务暂不可用", true)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 4*time.Second)
	defer cancel()
	audit, err := handler.service.Audit(ctx, ginContext.GetString("userName"))
	if err != nil {
		writeJudgeCalibrationError(ginContext, http.StatusServiceUnavailable, "JUDGE_CALIBRATION_ARTIFACT_UNAVAILABLE", "Judge 真实评分报告尚未生成或不可用", true)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, audit)
}

func (handler *JudgeCalibrationHandler) Review(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeJudgeCalibrationError(ginContext, http.StatusServiceUnavailable, "JUDGE_CALIBRATION_UNAVAILABLE", "Judge 校准服务暂不可用", true)
		return
	}
	ginContext.Request.Body = http.MaxBytesReader(ginContext.Writer, ginContext.Request.Body, maxJudgeCalibrationReviewBytes)
	request := new(JudgeCalibrationReviewRequest)
	decoder := json.NewDecoder(ginContext.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(request); err != nil || strings.TrimSpace(request.CaseID) == "" {
		writeJudgeCalibrationError(ginContext, http.StatusBadRequest, "INVALID_JUDGE_REVIEW", "人工评分参数错误", false)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJudgeCalibrationError(ginContext, http.StatusBadRequest, "INVALID_JUDGE_REVIEW", "人工评分参数错误", false)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 4*time.Second)
	defer cancel()
	receipt, err := handler.service.Submit(ctx, ginContext.GetString("userName"), strings.TrimSpace(request.CaseID), request.Scores)
	if err != nil {
		switch {
		case errors.Is(err, judgecalibration.ErrInvalidReview):
			writeJudgeCalibrationError(ginContext, http.StatusBadRequest, "INVALID_JUDGE_REVIEW", "五项评分只能使用 0、0.25、0.5、0.75 或 1", false)
		case errors.Is(err, judgecalibration.ErrCaseNotFound):
			writeJudgeCalibrationError(ginContext, http.StatusNotFound, "JUDGE_CALIBRATION_CASE_NOT_FOUND", "校准用例不存在", false)
		default:
			writeJudgeCalibrationError(ginContext, http.StatusServiceUnavailable, "JUDGE_REVIEW_PERSIST_FAILED", "人工评分暂未可靠写入", true)
		}
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusAccepted, receipt)
}

func writeJudgeCalibrationError(ginContext *gin.Context, status int, code, message string, retryable bool) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(status, ErrorResponse{SchemaVersion: judgecalibration.SchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}
