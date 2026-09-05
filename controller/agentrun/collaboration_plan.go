package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"GopherAI/internal/observability"
	"GopherAI/internal/orchestration"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

type CollaborationPlanner interface {
	Plan(context.Context, string) (orchestration.CollaborationPlan, error)
}

type CollaborationPlanObserver interface {
	RecordCollaborationPlan(string, string, time.Duration)
}

type CollaborationPlanHandler struct {
	planner  CollaborationPlanner
	observer CollaborationPlanObserver
}

type CollaborationPlanRequest struct {
	Message string `json:"message"`
}

func NewCollaborationPlanHandler(planner CollaborationPlanner, observer CollaborationPlanObserver) *CollaborationPlanHandler {
	return &CollaborationPlanHandler{planner: planner, observer: observer}
}

func NewDefaultCollaborationPlanHandler() *CollaborationPlanHandler {
	return NewCollaborationPlanHandler(orchestration.NewDefaultBoundedPlanner(), observability.DefaultMetrics())
}

func (handler *CollaborationPlanHandler) Plan(ctx *gin.Context) {
	startedAt := time.Now()
	if handler == nil || handler.planner == nil {
		handler.writeCollaborationPlanError(ctx, http.StatusServiceUnavailable, "COLLABORATION_PLANNER_UNAVAILABLE", "协作规划器暂时不可用", true)
		return
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maximumCaseShadowBodyBytes)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	request := new(CollaborationPlanRequest)
	if err := decoder.Decode(request); err != nil || caseRequestEOF(decoder) != nil || strings.TrimSpace(request.Message) == "" {
		handler.observeCollaborationPlan("error", "error", startedAt)
		handler.writeCollaborationPlanError(ctx, http.StatusBadRequest, "COLLABORATION_PLAN_REQUEST_INVALID", "请输入字段受限的故障现象 JSON", false)
		return
	}
	if strings.TrimSpace(ctx.GetString("userName")) == "" {
		handler.observeCollaborationPlan("error", "error", startedAt)
		handler.writeCollaborationPlanError(ctx, http.StatusUnauthorized, "COLLABORATION_PLAN_PRINCIPAL_MISSING", "无法确认当前登录用户", false)
		return
	}
	plan, err := handler.planner.Plan(ctx.Request.Context(), request.Message)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			handler.observeCollaborationPlan("cancelled", "error", startedAt)
			handler.writeCollaborationPlanError(ctx, 499, "COLLABORATION_PLAN_CANCELLED", "协作规划已取消", true)
			return
		}
		handler.observeCollaborationPlan("error", "error", startedAt)
		handler.writeCollaborationPlanError(ctx, http.StatusBadRequest, "COLLABORATION_PLAN_FAILED", "故障输入不足以形成安全计划", false)
		return
	}
	handler.observeCollaborationPlan(plan.Decision, plan.ReasonCode, startedAt)
	ctx.JSON(http.StatusOK, plan)
}

func (handler *CollaborationPlanHandler) observeCollaborationPlan(decision string, reason string, startedAt time.Time) {
	if handler != nil && handler.observer != nil {
		handler.observer.RecordCollaborationPlan(decision, reason, time.Since(startedAt))
	}
}

func (handler *CollaborationPlanHandler) writeCollaborationPlanError(ctx *gin.Context, status int, code string, message string, retryable bool) {
	_, traceID := requestid.IDs(ctx)
	ctx.JSON(status, ErrorResponse{SchemaVersion: orchestration.PlanSchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}
