package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"GopherAI/internal/observability"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const anomalyResponseSchemaVersion = "anomaly-simulation-response-v1"

type AnomalySimulationRequest struct {
	Scenario string `json:"scenario"`
}

type AnomalySimulationResponse struct {
	SchemaVersion string                        `json:"schema_version"`
	Simulation    bool                          `json:"simulation"`
	Source        string                        `json:"source"`
	Scenario      string                        `json:"scenario"`
	Analysis      observability.AnomalyAnalysis `json:"analysis"`
	Limitations   []string                      `json:"limitations"`
}

type ProductionAnomalyReader interface {
	LatestProductionAnalysis(context.Context) (observability.ProductionAnomalySnapshot, error)
}

type AnomalyHandler struct {
	now        func() time.Time
	production ProductionAnomalyReader
}

func NewAnomalyHandler() *AnomalyHandler {
	return &AnomalyHandler{now: time.Now, production: observability.NewDefaultMetricWindowService()}
}

func NewAnomalyHandlerWithProduction(reader ProductionAnomalyReader) *AnomalyHandler {
	return &AnomalyHandler{now: time.Now, production: reader}
}

func (handler *AnomalyHandler) Simulate(context *gin.Context) {
	if handler == nil || handler.now == nil {
		writeAnomalyError(context, http.StatusServiceUnavailable, "ANOMALY_DETECTOR_UNAVAILABLE", "异常检测器暂时不可用", true)
		return
	}
	context.Request.Body = http.MaxBytesReader(context.Writer, context.Request.Body, 4<<10)
	decoder := json.NewDecoder(context.Request.Body)
	decoder.DisallowUnknownFields()
	var request AnomalySimulationRequest
	if err := decoder.Decode(&request); err != nil {
		writeAnomalyError(context, http.StatusBadRequest, "INVALID_ANOMALY_SCENARIO", "异常场景请求无效", false)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeAnomalyError(context, http.StatusBadRequest, "INVALID_ANOMALY_SCENARIO", "异常场景请求包含多余内容", false)
		return
	}
	policy, observations, err := observability.AcceptanceAnomalyScenario(request.Scenario, handler.now().UTC())
	if err != nil {
		writeAnomalyError(context, http.StatusBadRequest, "UNKNOWN_ANOMALY_SCENARIO", "不支持的异常场景", false)
		return
	}
	analysis, err := observability.AnalyzeMetricWindow(policy, observations)
	if err != nil {
		writeAnomalyError(context, http.StatusUnprocessableEntity, "ANOMALY_ANALYSIS_FAILED", "异常窗口无法分析", false)
		return
	}
	context.JSON(http.StatusOK, AnomalySimulationResponse{
		SchemaVersion: anomalyResponseSchemaVersion, Simulation: true, Source: "deterministic_acceptance_fixture",
		Scenario: request.Scenario, Analysis: analysis,
		Limitations: []string{
			"这是确定性验收窗口，不冒充生产 Prometheus 观测。",
			"检测结果只生成 Recommend-only 候选，Applied 永远为 false。",
			"生产接线仍需独立 Prometheus recording rules、持久化事件仓库和签名 Webhook Worker。",
		},
	})
}

func (handler *AnomalyHandler) ProductionLatest(ginContext *gin.Context) {
	if handler == nil || handler.production == nil {
		writeProductionAnomalyError(ginContext)
		return
	}
	requestContext, cancel := context.WithTimeout(ginContext.Request.Context(), 3*time.Second)
	defer cancel()
	snapshot, err := handler.production.LatestProductionAnalysis(requestContext)
	if err != nil {
		writeProductionAnomalyError(ginContext)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, snapshot)
}

func writeProductionAnomalyError(context *gin.Context) {
	_, traceID := requestid.IDs(context)
	context.JSON(http.StatusServiceUnavailable, ErrorResponse{
		SchemaVersion: observability.ProductionAnomalySchemaVersion, Code: "PRODUCTION_ANOMALY_UNAVAILABLE",
		Message: "生产异常窗口暂不可用", Retryable: true, TraceID: traceID,
	})
}

func writeAnomalyError(context *gin.Context, status int, code string, message string, retryable bool) {
	_, traceID := requestid.IDs(context)
	context.JSON(status, ErrorResponse{SchemaVersion: anomalyResponseSchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}
