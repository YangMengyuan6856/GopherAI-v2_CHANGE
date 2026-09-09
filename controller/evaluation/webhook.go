package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"GopherAI/common/mysql"
	"GopherAI/internal/controlwebhook"
	"GopherAI/internal/observability"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

type ControlWebhookHandler struct {
	config     controlwebhook.Config
	receiver   *controlwebhook.Receiver
	repository *controlwebhook.GormRepository
}

type webhookAcceptanceRequest struct {
	Scenario string `json:"scenario"`
}

func NewDefaultControlWebhookHandler() *ControlWebhookHandler {
	config, _ := controlwebhook.DefaultConfig()
	repository := controlwebhook.NewGormRepository(mysql.DB)
	return &ControlWebhookHandler{config: config, receiver: controlwebhook.NewReceiver(config, repository), repository: repository}
}

// Receive is deliberately registered outside JWT middleware because a webhook
// sender authenticates with HMAC. The domain receiver still rejects non-loopback
// sources unless the explicit staging receiver is enabled.
func (handler *ControlWebhookHandler) Receive(ginContext *gin.Context) {
	if handler == nil || !handler.config.Enabled || !handler.config.LoopbackReceiver {
		ginContext.Status(http.StatusNotFound)
		return
	}
	ginContext.Request.Body = http.MaxBytesReader(ginContext.Writer, ginContext.Request.Body, 64<<10)
	body, err := io.ReadAll(ginContext.Request.Body)
	if err != nil {
		ginContext.JSON(http.StatusBadRequest, gin.H{"code": controlwebhook.ErrorInvalidPayload, "message": "Webhook 请求无效"})
		return
	}
	receiveContext, cancel := context.WithTimeout(ginContext.Request.Context(), 2*time.Second)
	defer cancel()
	duplicate, code, err := handler.receiver.Receive(receiveContext, ginContext.Request.RemoteAddr, ginContext.Request.Header, body)
	if err != nil {
		status := http.StatusUnauthorized
		if code == controlwebhook.ErrorInvalidPayload {
			status = http.StatusBadRequest
		} else if code == "WEBHOOK_RECEIPT_PERSIST_FAILED" {
			status = http.StatusServiceUnavailable
		}
		ginContext.JSON(status, gin.H{"code": code, "message": "Webhook 验证失败"})
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, gin.H{"schema_version": controlwebhook.SchemaVersion, "accepted": true, "duplicate": duplicate})
}

func (handler *ControlWebhookHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.repository == nil {
		writeWebhookAuditError(ginContext)
		return
	}
	requestContext, cancel := context.WithTimeout(ginContext.Request.Context(), 3*time.Second)
	defer cancel()
	snapshot, err := controlwebhook.LatestAudit(requestContext, handler.repository, handler.config)
	if err != nil {
		writeWebhookAuditError(ginContext)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, snapshot)
}

func (handler *ControlWebhookHandler) Acceptance(ginContext *gin.Context) {
	if handler == nil || !handler.config.Enabled || !handler.config.LoopbackReceiver || handler.repository == nil {
		ginContext.JSON(http.StatusConflict, gin.H{"code": "WEBHOOK_ACCEPTANCE_DISABLED", "message": "当前环境未启用签名回环验收"})
		return
	}
	ginContext.Request.Body = http.MaxBytesReader(ginContext.Writer, ginContext.Request.Body, 4<<10)
	decoder := json.NewDecoder(ginContext.Request.Body)
	decoder.DisallowUnknownFields()
	var request webhookAcceptanceRequest
	if err := decoder.Decode(&request); err != nil || request.Scenario != controlwebhook.AcceptanceFixtureMode {
		ginContext.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_WEBHOOK_ACCEPTANCE_SCENARIO", "message": "Webhook 验收场景无效"})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		ginContext.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_WEBHOOK_ACCEPTANCE_SCENARIO", "message": "Webhook 验收请求包含多余内容"})
		return
	}
	runtimeContext, cancel := context.WithTimeout(ginContext.Request.Context(), 3*time.Second)
	defer cancel()
	runtimeSnapshot, err := observability.NewDefaultPrometheusRuntimeClient().Snapshot(runtimeContext)
	if err != nil {
		ginContext.JSON(http.StatusServiceUnavailable, gin.H{"code": "PROMETHEUS_PROVENANCE_UNAVAILABLE", "message": "生产 Prometheus 口径暂不可用"})
		return
	}
	delivery, err := controlwebhook.BuildAcceptanceDelivery(runtimeSnapshot, time.Now().UTC())
	if err != nil || handler.repository.EnqueueAcceptanceDelivery(runtimeContext, delivery) != nil {
		ginContext.JSON(http.StatusServiceUnavailable, gin.H{"code": "WEBHOOK_ACCEPTANCE_ENQUEUE_FAILED", "message": "Webhook 验收事件入队失败"})
		return
	}
	ginContext.JSON(http.StatusAccepted, gin.H{
		"schema_version": controlwebhook.SchemaVersion, "simulation": true, "event_id": delivery.EventID,
		"status": delivery.Status, "message": "签名验收事件已异步入队，不会修改活动策略",
	})
}

func writeWebhookAuditError(ginContext *gin.Context) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(http.StatusServiceUnavailable, ErrorResponse{
		SchemaVersion: controlwebhook.SchemaVersion, Code: "WEBHOOK_AUDIT_UNAVAILABLE",
		Message: "Webhook 审计暂不可用", Retryable: true, TraceID: traceID,
	})
}
