package evaluation

import (
	"context"
	"net/http"
	"time"

	"GopherAI/internal/cleanupaudit"
	"GopherAI/internal/observability"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

const defaultCleanupAuditPath = "/root/GopherAI_Runtime/evaluation/cleanup-audit-latest.json"

type CleanupAuditRunner func(context.Context) (cleanupaudit.Report, error)

type CleanupAuditHandler struct {
	run   CleanupAuditRunner
	store cleanupaudit.Store
}

func NewCleanupAuditHandler(run CleanupAuditRunner, store cleanupaudit.Store) *CleanupAuditHandler {
	return &CleanupAuditHandler{run: run, store: store}
}

func NewDefaultCleanupAuditHandler() *CleanupAuditHandler {
	builder := cleanupaudit.NewBuilder(".", observability.NewDefaultPrometheusRuntimeClient(), time.Now)
	run := func(ctx context.Context) (cleanupaudit.Report, error) {
		manifest, _, err := loadInterviewReleaseManifest(defaultReleaseManifestPath)
		if err != nil {
			return cleanupaudit.Report{}, err
		}
		return builder.Build(ctx, manifest.ReleaseID, manifest.GitSHA)
	}
	return NewCleanupAuditHandler(run, cleanupaudit.NewFileStore(defaultCleanupAuditPath))
}

func (handler *CleanupAuditHandler) Latest(ginContext *gin.Context) {
	if handler == nil || handler.store == nil {
		writeCleanupAuditError(ginContext, http.StatusServiceUnavailable, "CLEANUP_AUDIT_UNAVAILABLE", "清理审计暂不可用", true)
		return
	}
	report, err := handler.store.Load()
	if err != nil {
		writeCleanupAuditError(ginContext, http.StatusNotFound, "CLEANUP_AUDIT_NOT_READY", "请先运行清理前依赖与零调用审计", false)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, report)
}

func (handler *CleanupAuditHandler) Acceptance(ginContext *gin.Context) {
	if handler == nil || handler.run == nil || handler.store == nil {
		writeCleanupAuditError(ginContext, http.StatusServiceUnavailable, "CLEANUP_AUDIT_UNAVAILABLE", "清理审计暂不可用", true)
		return
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 8*time.Second)
	defer cancel()
	report, err := handler.run(ctx)
	if err != nil {
		writeCleanupAuditError(ginContext, http.StatusUnprocessableEntity, "CLEANUP_AUDIT_FAILED", "清理审计未能形成可信报告", false)
		return
	}
	if err := handler.store.Save(report); err != nil {
		writeCleanupAuditError(ginContext, http.StatusServiceUnavailable, "CLEANUP_AUDIT_PERSIST_FAILED", "清理审计报告无法安全落盘", true)
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, report)
}

func writeCleanupAuditError(ginContext *gin.Context, status int, code, message string, retryable bool) {
	_, traceID := requestid.IDs(ginContext)
	ginContext.JSON(status, ErrorResponse{SchemaVersion: cleanupaudit.SchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}
