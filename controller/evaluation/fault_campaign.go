package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"GopherAI/internal/faultcampaign"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

type FaultCampaignService interface {
	Audit(context.Context) (faultcampaign.AuditSnapshot, error)
	RunAcceptance(context.Context, string) (faultcampaign.CampaignReport, error)
}

type FaultCampaignHandler struct{ service FaultCampaignService }

type faultCampaignRequest struct {
	Scenario string `json:"scenario"`
}

func NewFaultCampaignHandler(service FaultCampaignService) *FaultCampaignHandler {
	return &FaultCampaignHandler{service: service}
}

func NewDefaultFaultCampaignHandler() *FaultCampaignHandler {
	service, err := faultcampaign.NewDefaultService()
	if err != nil {
		return &FaultCampaignHandler{}
	}
	return NewFaultCampaignHandler(service)
}

func (handler *FaultCampaignHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeFaultCampaignError(ginContext, http.StatusServiceUnavailable, "FAULT_CAMPAIGN_UNAVAILABLE", "故障演练服务暂不可用", true)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 3*time.Second)
	defer cancel()
	snapshot, err := handler.service.Audit(ctx)
	if err != nil {
		writeFaultCampaignError(ginContext, http.StatusServiceUnavailable, "FAULT_CAMPAIGN_AUDIT_UNAVAILABLE", "故障演练审计暂不可用", true)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, snapshot)
}

func (handler *FaultCampaignHandler) Acceptance(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeFaultCampaignError(ginContext, http.StatusServiceUnavailable, "FAULT_CAMPAIGN_UNAVAILABLE", "故障演练服务暂不可用", true)
		return
	}
	ginContext.Request.Body = http.MaxBytesReader(ginContext.Writer, ginContext.Request.Body, 4<<10)
	decoder := json.NewDecoder(ginContext.Request.Body)
	decoder.DisallowUnknownFields()
	var request faultCampaignRequest
	if err := decoder.Decode(&request); err != nil {
		writeFaultCampaignError(ginContext, http.StatusBadRequest, "INVALID_FAULT_CAMPAIGN", "故障演练请求无效", false)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeFaultCampaignError(ginContext, http.StatusBadRequest, "INVALID_FAULT_CAMPAIGN", "故障演练请求包含多余内容", false)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 5*time.Second)
	defer cancel()
	report, err := handler.service.RunAcceptance(ctx, request.Scenario)
	if err != nil {
		writeFaultCampaignError(ginContext, http.StatusUnprocessableEntity, "FAULT_CAMPAIGN_FAILED", "三类故障演练未通过", false)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, report)
}

func writeFaultCampaignError(ginContext *gin.Context, status int, code, message string, retryable bool) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(status, ErrorResponse{SchemaVersion: faultcampaign.SchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}
