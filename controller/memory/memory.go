package memory

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"GopherAI/common/mysql"
	memorydomain "GopherAI/internal/memory"
	profiledomain "GopherAI/internal/profilememory"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

type MemoryService interface {
	Preview(context.Context, string, string, int) (memorydomain.Preview, error)
	Rebuild(context.Context, string, string) (memorydomain.WorkingWindow, error)
}

type ProfileService interface {
	List(context.Context, string) (profiledomain.ListResponse, error)
	Correct(context.Context, profiledomain.Correction) (profiledomain.PublicMemory, error)
	Delete(context.Context, string, string) error
}

type Handler struct {
	service  MemoryService
	profiles ProfileService
}

type ProfileCorrectionRequest struct {
	Value         string `json:"value" binding:"required"`
	ExpiresInDays *int   `json:"expires_in_days,omitempty"`
}

type ErrorResponse struct {
	SchemaVersion string `json:"schema_version"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Retryable     bool   `json:"retryable"`
	TraceID       string `json:"trace_id,omitempty"`
}

func NewHandler(service MemoryService) *Handler { return &Handler{service: service} }

func NewHandlerWithProfiles(service MemoryService, profiles ProfileService) *Handler {
	return &Handler{service: service, profiles: profiles}
}

func NewDefaultHandler() *Handler {
	profiles, err := profiledomain.NewService(profiledomain.NewGormRepository(mysql.DB), profiledomain.SystemClock{})
	if err != nil {
		panic(err)
	}
	return NewHandlerWithProfiles(memorydomain.NewDefaultService(), profiles)
}

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

func (handler *Handler) ListProfiles(context *gin.Context) {
	if handler.profiles == nil {
		handler.writeProfileError(context, http.StatusServiceUnavailable, "PROFILE_MEMORY_UNAVAILABLE", "环境记忆暂时不可用", true)
		return
	}
	response, err := handler.profiles.List(context.Request.Context(), context.GetString("userName"))
	if err != nil {
		handler.handleProfileError(context, err)
		return
	}
	context.JSON(http.StatusOK, response)
}

func (handler *Handler) CorrectProfile(context *gin.Context) {
	if handler.profiles == nil {
		handler.writeProfileError(context, http.StatusServiceUnavailable, "PROFILE_MEMORY_UNAVAILABLE", "环境记忆暂时不可用", true)
		return
	}
	request := new(ProfileCorrectionRequest)
	if err := context.ShouldBindJSON(request); err != nil {
		handler.writeProfileError(context, http.StatusBadRequest, "INVALID_PROFILE_MEMORY", "环境记忆更正参数不合法", false)
		return
	}
	result, err := handler.profiles.Correct(context.Request.Context(), profiledomain.Correction{
		MemoryID: context.Param("memory_id"), UserID: context.GetString("userName"), Value: request.Value, ExpiresInDays: request.ExpiresInDays,
	})
	if err != nil {
		handler.handleProfileError(context, err)
		return
	}
	context.JSON(http.StatusOK, gin.H{"schema_version": profiledomain.SchemaVersion, "memory": result})
}

func (handler *Handler) DeleteProfile(context *gin.Context) {
	if handler.profiles == nil {
		handler.writeProfileError(context, http.StatusServiceUnavailable, "PROFILE_MEMORY_UNAVAILABLE", "环境记忆暂时不可用", true)
		return
	}
	if err := handler.profiles.Delete(context.Request.Context(), context.GetString("userName"), context.Param("memory_id")); err != nil {
		handler.handleProfileError(context, err)
		return
	}
	context.Status(http.StatusNoContent)
	context.Writer.WriteHeaderNow()
}

func (handler *Handler) handleServiceError(context *gin.Context, err error) {
	if errors.Is(err, memorydomain.ErrSessionNotFound) {
		handler.writeError(context, http.StatusNotFound, "MEMORY_SESSION_NOT_FOUND", "未找到该会话", false)
		return
	}
	handler.writeError(context, http.StatusServiceUnavailable, "MEMORY_CONTEXT_UNAVAILABLE", "上下文记忆暂时不可用", true)
}

func (handler *Handler) handleProfileError(context *gin.Context, err error) {
	switch {
	case errors.Is(err, profiledomain.ErrInvalidProfileMemory):
		handler.writeProfileError(context, http.StatusBadRequest, "INVALID_PROFILE_MEMORY", "环境记忆内容或有效期不合法", false)
	case errors.Is(err, profiledomain.ErrProfileMemoryNotFound):
		handler.writeProfileError(context, http.StatusNotFound, "PROFILE_MEMORY_NOT_FOUND", "未找到该环境记忆", false)
	default:
		handler.writeProfileError(context, http.StatusServiceUnavailable, "PROFILE_MEMORY_UNAVAILABLE", "环境记忆暂时不可用", true)
	}
}

func (handler *Handler) writeProfileError(context *gin.Context, status int, code string, message string, retryable bool) {
	_, traceID := requestid.IDs(context)
	context.JSON(status, ErrorResponse{SchemaVersion: profiledomain.SchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}

func (handler *Handler) writeError(context *gin.Context, status int, code string, message string, retryable bool) {
	_, traceID := requestid.IDs(context)
	context.JSON(status, ErrorResponse{SchemaVersion: memorydomain.SchemaVersion, Code: code, Message: message, Retryable: retryable, TraceID: traceID})
}
