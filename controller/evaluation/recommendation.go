package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"GopherAI/internal/controlrecommendation"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

type RecommendationController interface {
	Audit(context.Context) (controlrecommendation.AuditSnapshot, error)
	Acceptance(context.Context, string) (controlrecommendation.AcceptanceResult, error)
}

type RecommendationHandler struct{ controller RecommendationController }

type recommendationAcceptanceRequest struct {
	Scenario string `json:"scenario"`
}

func NewRecommendationHandler(controller RecommendationController) *RecommendationHandler {
	return &RecommendationHandler{controller: controller}
}

func NewDefaultRecommendationHandler() *RecommendationHandler {
	controller, err := controlrecommendation.NewDefaultController()
	if err != nil {
		return &RecommendationHandler{}
	}
	return NewRecommendationHandler(controller)
}

func (handler *RecommendationHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.controller == nil {
		writeRecommendationError(ginContext, http.StatusServiceUnavailable, "CONTROLLER_UNAVAILABLE", "只建议控制器暂不可用", true)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 3*time.Second)
	defer cancel()
	snapshot, err := handler.controller.Audit(ctx)
	if err != nil {
		writeRecommendationError(ginContext, http.StatusServiceUnavailable, "CONTROLLER_AUDIT_UNAVAILABLE", "控制器审计暂不可用", true)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, snapshot)
}

func (handler *RecommendationHandler) Acceptance(ginContext *gin.Context) {
	if handler == nil || handler.controller == nil {
		writeRecommendationError(ginContext, http.StatusServiceUnavailable, "CONTROLLER_UNAVAILABLE", "只建议控制器暂不可用", true)
		return
	}
	ginContext.Request.Body = http.MaxBytesReader(ginContext.Writer, ginContext.Request.Body, 4<<10)
	decoder := json.NewDecoder(ginContext.Request.Body)
	decoder.DisallowUnknownFields()
	var request recommendationAcceptanceRequest
	if err := decoder.Decode(&request); err != nil {
		writeRecommendationError(ginContext, http.StatusBadRequest, "INVALID_CONTROLLER_ACCEPTANCE", "控制器验收请求无效", false)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeRecommendationError(ginContext, http.StatusBadRequest, "INVALID_CONTROLLER_ACCEPTANCE", "控制器验收请求包含多余内容", false)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 5*time.Second)
	defer cancel()
	result, err := handler.controller.Acceptance(ctx, request.Scenario)
	if err != nil {
		writeRecommendationError(ginContext, http.StatusUnprocessableEntity, "CONTROLLER_ACCEPTANCE_FAILED", "只建议控制器验收未通过", false)
		return
	}
	ginContext.JSON(http.StatusOK, result)
}

func writeRecommendationError(ginContext *gin.Context, status int, code string, message string, retryable bool) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(status, ErrorResponse{SchemaVersion: controlrecommendation.SchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}
