package memory

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	memorydomain "GopherAI/internal/memory"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

type MemoryService interface {
	Preview(context.Context, string, string, int) (memorydomain.Preview, error)
	Rebuild(context.Context, string, string) (memorydomain.WorkingWindow, error)
}

type Handler struct {
	service MemoryService
}

type ErrorResponse struct {
	SchemaVersion string `json:"schema_version"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Retryable     bool   `json:"retryable"`
	TraceID       string `json:"trace_id,omitempty"`
}

func NewHandler(service MemoryService) *Handler { return &Handler{service: service} }

func NewDefaultHandler() *Handler { return NewHandler(memorydomain.NewDefaultService()) }

func (handler *Handler) Preview(context *gin.Context) {
	budget := 0
	if raw := strings.TrimSpace(context.Query("budget_tokens")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 64 || parsed > memorydomain.MaxTokenBudget {
			handler.writeError(context, http.StatusBadRequest, "INVALID_MEMORY_BUDGET", "上下文预算必须是 64 到 8192 之间的整数", false)
			return
		}
		budget = parsed
	}
	preview, err := handler.service.Preview(context.Request.Context(), context.GetString("userName"), context.Param("session_id"), budget)
	if err != nil {
		handler.handleServiceError(context, err)
		return
	}
	context.JSON(http.StatusOK, preview)
}

func (handler *Handler) Rebuild(context *gin.Context) {
	window, err := handler.service.Rebuild(context.Request.Context(), context.GetString("userName"), context.Param("session_id"))
	if err != nil {
		handler.handleServiceError(context, err)
		return
	}
	context.JSON(http.StatusOK, gin.H{
		"schema_version": memorydomain.SchemaVersion,
		"message":        "Redis 工作记忆已从 MySQL 权威消息安全重建",
		"window":         window,
	})
}

func (handler *Handler) handleServiceError(context *gin.Context, err error) {
	if errors.Is(err, memorydomain.ErrSessionNotFound) {
		handler.writeError(context, http.StatusNotFound, "MEMORY_SESSION_NOT_FOUND", "未找到该会话", false)
		return
	}
	handler.writeError(context, http.StatusServiceUnavailable, "MEMORY_CONTEXT_UNAVAILABLE", "上下文记忆暂时不可用", true)
}

func (handler *Handler) writeError(context *gin.Context, status int, code string, message string, retryable bool) {
	_, traceID := requestid.IDs(context)
	context.JSON(status, ErrorResponse{SchemaVersion: memorydomain.SchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}
