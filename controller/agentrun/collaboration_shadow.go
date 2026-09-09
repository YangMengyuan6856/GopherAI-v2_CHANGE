package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"GopherAI/common/mysql"
	"GopherAI/internal/diagnostic"
	"GopherAI/internal/incident"
	"GopherAI/internal/observability"
	"GopherAI/internal/orchestration"
	knowledgeapp "GopherAI/internal/platform/knowledge"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

type CollaborationShadowRunner interface {
	Run(context.Context, orchestration.ExecutionInput) (orchestration.CollaborationRun, error)
}

type CollaborationShadowObserver interface {
	RecordCollaborationPlan(string, string, time.Duration)
	RecordCollaborationRun(string, string, time.Duration)
	RecordCollaborationTask(string, string, time.Duration)
	RecordCollaborationSynthesis(string, string)
}

type CollaborationShadowHandler struct {
	runner   CollaborationShadowRunner
	observer CollaborationShadowObserver
}

func NewCollaborationShadowHandler(runner CollaborationShadowRunner, observer CollaborationShadowObserver) *CollaborationShadowHandler {
	return &CollaborationShadowHandler{runner: runner, observer: observer}
}

func NewDefaultCollaborationShadowHandler() *CollaborationShadowHandler {
	knowledgeRunner, err := orchestration.NewKnowledgeRunner(knowledgeapp.NewLazyDefaultAnswerer())
	if err != nil {
		panic(err)
	}
	caseStrategy, err := diagnostic.NewCaseBasedStrategy(diagnostic.NewAgent(), incident.NewGormRepository(mysql.DB), 1200*time.Millisecond)
	if err != nil {
		panic(err)
	}
	diagnosticRunner, err := orchestration.NewDiagnosticRunner(caseStrategy)
	if err != nil {
		panic(err)
	}
	executor, err := orchestration.NewParallelExecutor(map[string]orchestration.AgentRunner{
		orchestration.KnowledgeAgentRole: knowledgeRunner, orchestration.DiagnosticAgentRole: diagnosticRunner,
	})
	if err != nil {
		panic(err)
	}
	coordinator, err := orchestration.NewShadowCoordinator(
		orchestration.NewDefaultBoundedPlanner(), executor, orchestration.NewEvidenceAwareSynthesizer(),
	)
	if err != nil {
		panic(err)
	}
	return NewCollaborationShadowHandler(coordinator, observability.DefaultMetrics())
}

func (handler *CollaborationShadowHandler) Run(ctx *gin.Context) {
	startedAt := time.Now()
	if handler == nil || handler.runner == nil {
		handler.writeError(ctx, http.StatusServiceUnavailable, "COLLABORATION_SHADOW_UNAVAILABLE", "协作 Shadow 暂时不可用", true)
		return
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maximumCaseShadowBodyBytes)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	request := new(CollaborationPlanRequest)
	if err := decoder.Decode(request); err != nil || caseRequestEOF(decoder) != nil || strings.TrimSpace(request.Message) == "" {
		handler.observeFailure("error", "failed", startedAt)
		handler.writeError(ctx, http.StatusBadRequest, "COLLABORATION_SHADOW_REQUEST_INVALID", "请输入字段受限的故障现象 JSON", false)
		return
	}
	userID := strings.TrimSpace(ctx.GetString("userName"))
	if userID == "" {
		handler.observeFailure("error", "failed", startedAt)
		handler.writeError(ctx, http.StatusUnauthorized, "COLLABORATION_SHADOW_PRINCIPAL_MISSING", "无法确认当前登录用户", false)
		return
	}
	result, err := handler.runner.Run(ctx.Request.Context(), orchestration.ExecutionInput{
		TenantID: userID, UserID: userID, Message: request.Message,
	})
	_, result.TraceID = requestid.IDs(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			handler.observeResult(result, "cancelled", startedAt)
			handler.writeError(ctx, 499, "COLLABORATION_SHADOW_CANCELLED", "协作 Shadow 已取消", true)
			return
		}
		handler.observeResult(result, "failed", startedAt)
		handler.writeError(ctx, http.StatusServiceUnavailable, "COLLABORATION_SHADOW_FAILED", "协作 Shadow 暂时失败", true)
		return
	}
	handler.observeResult(result, result.Status, startedAt)
	ctx.JSON(http.StatusOK, result)
}

func (handler *CollaborationShadowHandler) observeFailure(decision string, status string, startedAt time.Time) {
	if handler != nil && handler.observer != nil {
		handler.observer.RecordCollaborationRun(decision, status, time.Since(startedAt))
	}
}

func (handler *CollaborationShadowHandler) observeResult(result orchestration.CollaborationRun, status string, startedAt time.Time) {
	if handler == nil || handler.observer == nil {
		return
	}
	decision := result.Plan.Decision
	if decision == "" {
		decision = "error"
	}
	handler.observer.RecordCollaborationPlan(decision, result.Plan.ReasonCode, 0)
	handler.observer.RecordCollaborationRun(decision, status, time.Since(startedAt))
	if result.Execution != nil {
		for _, task := range result.Execution.TaskResults {
			handler.observer.RecordCollaborationTask(task.Agent, task.Status, time.Duration(task.DurationMS)*time.Millisecond)
		}
	}
	if result.Synthesis != nil {
		handler.observer.RecordCollaborationSynthesis(result.Synthesis.Status, result.Synthesis.ReasonCode)
	}
}

func (handler *CollaborationShadowHandler) writeError(ctx *gin.Context, status int, code string, message string, retryable bool) {
	_, traceID := requestid.IDs(ctx)
	ctx.JSON(status, ErrorResponse{SchemaVersion: orchestration.CollaborationRunSchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}
