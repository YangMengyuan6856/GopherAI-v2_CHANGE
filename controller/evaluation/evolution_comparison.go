package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"GopherAI/internal/evolution"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const defaultEvolutionComparisonPath = "/root/GopherAI_Runtime/evaluation/harness-evolution-comparison-latest.json"

type EvolutionComparisonRunner func(context.Context, *evolution.ComparisonReport, time.Time) (evolution.ComparisonReport, bool, error)

type EvolutionComparisonHandler struct {
	run   EvolutionComparisonRunner
	store evolution.ComparisonStore
}

type evolutionComparisonRequest struct {
	Mode string `json:"mode"`
}

type evolutionComparisonResponse struct {
	Created bool                       `json:"created"`
	Reused  bool                       `json:"reused"`
	Report  evolution.ComparisonReport `json:"report"`
}

func NewEvolutionComparisonHandler(run EvolutionComparisonRunner, store evolution.ComparisonStore) *EvolutionComparisonHandler {
	return &EvolutionComparisonHandler{run: run, store: store}
}

func NewDefaultEvolutionComparisonHandler() *EvolutionComparisonHandler {
	runner := func(ctx context.Context, existing *evolution.ComparisonReport, now time.Time) (evolution.ComparisonReport, bool, error) {
		return evolution.RunFairComparison(ctx, defaultEvolutionDatasetPath, defaultEvolutionCatalogPath, existing, now)
	}
	return NewEvolutionComparisonHandler(runner, evolution.NewFileComparisonStore(defaultEvolutionComparisonPath))
}

func (handler *EvolutionComparisonHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.store == nil {
		writeEvolutionComparisonError(ginContext, http.StatusServiceUnavailable, "EVOLUTION_COMPARISON_UNAVAILABLE", "Harness 公平比较报告暂不可用", true)
		return
	}
	report, err := handler.store.Load()
	if err != nil {
		writeEvolutionComparisonError(ginContext, http.StatusNotFound, "EVOLUTION_COMPARISON_NOT_READY", "请先运行 Harness 公平比较", false)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, report)
}

func (handler *EvolutionComparisonHandler) Run(ginContext *gin.Context) {
	if handler == nil || handler.run == nil || handler.store == nil {
		writeEvolutionComparisonError(ginContext, http.StatusServiceUnavailable, "EVOLUTION_COMPARISON_UNAVAILABLE", "Harness 公平比较暂不可用", true)
		return
	}
	ginContext.Request.Body = http.MaxBytesReader(ginContext.Writer, ginContext.Request.Body, 1<<10)
	decoder := json.NewDecoder(ginContext.Request.Body)
	decoder.DisallowUnknownFields()
	var request evolutionComparisonRequest
	if err := decoder.Decode(&request); err != nil || request.Mode != "controlled_offline_contract_comparison" {
		writeEvolutionComparisonError(ginContext, http.StatusBadRequest, "INVALID_EVOLUTION_COMPARISON_MODE", "只允许受控离线 Harness 公平比较", false)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeEvolutionComparisonError(ginContext, http.StatusBadRequest, "INVALID_EVOLUTION_COMPARISON_MODE", "Harness 公平比较请求包含多余内容", false)
		return
	}
	var existing *evolution.ComparisonReport
	loaded, err := handler.store.Load()
	if err == nil {
		existing = &loaded
	} else if !errors.Is(err, os.ErrNotExist) {
		writeEvolutionComparisonError(ginContext, http.StatusServiceUnavailable, "EVOLUTION_COMPARISON_REPORT_INVALID", "既有 Harness 公平比较报告校验失败", false)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 8*time.Second)
	defer cancel()
	report, reused, err := handler.run(ctx, existing, time.Now().UTC())
	if err != nil {
		writeEvolutionComparisonError(ginContext, http.StatusUnprocessableEntity, "EVOLUTION_COMPARISON_FAILED", "Harness 公平比较未完成", false)
		return
	}
	if !reused {
		if err := handler.store.Save(report); err != nil {
			writeEvolutionComparisonError(ginContext, http.StatusServiceUnavailable, "EVOLUTION_COMPARISON_PERSIST_FAILED", "Harness 公平比较报告未能安全落盘", true)
			return
		}
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, evolutionComparisonResponse{Created: !reused, Reused: reused, Report: report})
}

func writeEvolutionComparisonError(ginContext *gin.Context, status int, code, message string, retryable bool) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(status, ErrorResponse{SchemaVersion: evolution.ComparisonSchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}
