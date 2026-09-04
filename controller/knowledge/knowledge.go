package knowledge

import (
	"GopherAI/common/mysql"
	"GopherAI/internal/contract"
	knowledgeapp "GopherAI/internal/knowledge"
	"GopherAI/internal/observability"
	"GopherAI/middleware/requestid"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

type Application interface {
	Accept(ctx context.Context, input knowledgeapp.AcceptInput) (knowledgeapp.AcceptResult, error)
	List(ctx context.Context, tenantID string) ([]knowledgeapp.DocumentSummary, error)
	Job(ctx context.Context, tenantID string, jobID string) (knowledgeapp.JobSummary, error)
}

type UploadMetrics interface {
	RecordDocumentUpload(status string, sizeBytes int64)
}

type Handler struct {
	application Application
	metrics     UploadMetrics
}

type uploadResponse struct {
	SchemaVersion string                       `json:"schema_version"`
	TraceID       string                       `json:"trace_id"`
	Duplicate     bool                         `json:"duplicate"`
	Document      knowledgeapp.DocumentSummary `json:"document"`
	Job           knowledgeapp.JobSummary      `json:"job"`
}

type listResponse struct {
	SchemaVersion string                         `json:"schema_version"`
	TraceID       string                         `json:"trace_id"`
	Documents     []knowledgeapp.DocumentSummary `json:"documents"`
}

type jobResponse struct {
	SchemaVersion string                  `json:"schema_version"`
	TraceID       string                  `json:"trace_id"`
	Job           knowledgeapp.JobSummary `json:"job"`
}

func NewHandler(application Application, metrics UploadMetrics) *Handler {
	return &Handler{application: application, metrics: metrics}
}

func NewDefaultHandler() *Handler {
	repository := knowledgeapp.NewGormRepository(mysql.DB)
	return NewHandler(knowledgeapp.NewDefaultService(repository), observability.DefaultMetrics())
}

func (handler *Handler) Upload(context *gin.Context) {
	fileHeader, err := context.FormFile("file")
	if err != nil {
		handler.recordUpload("rejected", 0)
		handler.writeError(context, contract.NewDomainError("DOCUMENT_REQUIRED", contract.ErrorValidation, "请选择要上传的文档", false, err))
		return
	}
	_, traceID := requestid.IDs(context)
	userID := context.GetString("userName")
	result, err := handler.application.Accept(context.Request.Context(), knowledgeapp.AcceptInput{
		TenantID: userID,
		UserID:   userID,
		TraceID:  traceID,
		File:     fileHeader,
	})
	if err != nil {
		status := "error"
		var domainError *contract.DomainError
		if errors.As(err, &domainError) && domainError.Category == contract.ErrorValidation {
			status = "rejected"
		}
		handler.recordUpload(status, 0)
		handler.writeError(context, err)
		return
	}
	status := "accepted"
	if result.Duplicate {
		status = "duplicate"
	}
	handler.recordUpload(status, result.Document.SizeBytes)
	writeUploadLog(traceID, result, status)
	context.JSON(http.StatusAccepted, uploadResponse{
		SchemaVersion: contract.SchemaVersion,
		TraceID:       traceID,
		Duplicate:     result.Duplicate,
		Document:      result.Document,
		Job:           result.Job,
	})
}

func (handler *Handler) List(context *gin.Context) {
	_, traceID := requestid.IDs(context)
	documents, err := handler.application.List(context.Request.Context(), context.GetString("userName"))
	if err != nil {
		handler.writeError(context, err)
		return
	}
	context.JSON(http.StatusOK, listResponse{SchemaVersion: contract.SchemaVersion, TraceID: traceID, Documents: documents})
}

func (handler *Handler) Job(context *gin.Context) {
	_, traceID := requestid.IDs(context)
	job, err := handler.application.Job(context.Request.Context(), context.GetString("userName"), context.Param("job_id"))
	if err != nil {
		handler.writeError(context, err)
		return
	}
	context.JSON(http.StatusOK, jobResponse{SchemaVersion: contract.SchemaVersion, TraceID: traceID, Job: job})
}

func (handler *Handler) writeError(context *gin.Context, err error) {
	_, traceID := requestid.IDs(context)
	domainError := contract.WithTrace(err, traceID)
	context.JSON(httpStatus(domainError.Category), domainError)
}

func (handler *Handler) recordUpload(status string, sizeBytes int64) {
	if handler.metrics != nil {
		handler.metrics.RecordDocumentUpload(status, sizeBytes)
	}
}

func httpStatus(category contract.ErrorCategory) int {
	switch category {
	case contract.ErrorValidation:
		return http.StatusBadRequest
	case contract.ErrorAuth:
		return http.StatusUnauthorized
	case contract.ErrorNotFound:
		return http.StatusNotFound
	case contract.ErrorConflict:
		return http.StatusConflict
	case contract.ErrorDependencyTimeout:
		return http.StatusGatewayTimeout
	case contract.ErrorDependencyUnavailable, contract.ErrorModel:
		return http.StatusServiceUnavailable
	case contract.ErrorBudgetExceeded:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

func writeUploadLog(traceID string, result knowledgeapp.AcceptResult, status string) {
	record := map[string]any{
		"event":       "knowledge_document_upload",
		"trace_id":    traceID,
		"document_id": result.Document.ID,
		"job_id":      result.Job.ID,
		"status":      status,
		"size_bytes":  result.Document.SizeBytes,
	}
	encoded, err := json.Marshal(record)
	if err == nil {
		log.Print(string(encoded))
	}
}
