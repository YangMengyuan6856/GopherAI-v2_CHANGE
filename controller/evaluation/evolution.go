package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"GopherAI/internal/evolution"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

type EvolutionService interface {
	Audit(context.Context) (evolution.Audit, error)
	MaterializeLatest(context.Context) (evolution.MaterializeResult, error)
}

type EvolutionHandler struct{ service EvolutionService }

type evolutionMaterializeRequest struct {
	Source string `json:"source"`
}

func NewEvolutionHandler(service EvolutionService) *EvolutionHandler {
	return &EvolutionHandler{service: service}
}

func NewDefaultEvolutionHandler() *EvolutionHandler {
	service, err := evolution.NewDefaultService()
	if err != nil {
		return &EvolutionHandler{}
	}
	return NewEvolutionHandler(service)
}

func (handler *EvolutionHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeEvolutionError(ginContext, http.StatusServiceUnavailable, "EVOLUTION_UNAVAILABLE", "Harness 候选谱系暂不可用", true)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 4*time.Second)
	defer cancel()
	audit, err := handler.service.Audit(ctx)
	if err != nil {
		writeEvolutionError(ginContext, http.StatusServiceUnavailable, "EVOLUTION_AUDIT_FAILED", "Harness 候选谱系读取失败", true)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, audit)
}

func (handler *EvolutionHandler) Materialize(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeEvolutionError(ginContext, http.StatusServiceUnavailable, "EVOLUTION_UNAVAILABLE", "Harness 候选谱系暂不可用", true)
		return
	}
	ginContext.Request.Body = http.MaxBytesReader(ginContext.Writer, ginContext.Request.Body, 4<<10)
	decoder := json.NewDecoder(ginContext.Request.Body)
	decoder.DisallowUnknownFields()
	var request evolutionMaterializeRequest
	if err := decoder.Decode(&request); err != nil || request.Source != "latest_failure_pool" {
		writeEvolutionError(ginContext, http.StatusBadRequest, "INVALID_EVOLUTION_SOURCE", "只允许从当前脱敏失败池生成离线候选", false)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeEvolutionError(ginContext, http.StatusBadRequest, "INVALID_EVOLUTION_SOURCE", "Harness 候选请求包含多余内容", false)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 8*time.Second)
	defer cancel()
	result, err := handler.service.MaterializeLatest(ctx)
	if err != nil {
		writeEvolutionError(ginContext, http.StatusUnprocessableEntity, "EVOLUTION_MATERIALIZE_FAILED", "当前失败池无法生成合法 Harness 候选", false)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, result)
}

func writeEvolutionError(ginContext *gin.Context, status int, code, message string, retryable bool) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(status, ErrorResponse{SchemaVersion: evolution.SchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}
