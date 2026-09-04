package knowledge

import (
	"GopherAI/common/mysql"
	redisstore "GopherAI/common/redis"
	"GopherAI/config"
	"GopherAI/internal/contract"
	knowledgeapp "GopherAI/internal/knowledge"
	"GopherAI/internal/observability"
	ragapp "GopherAI/internal/rag"
	"GopherAI/middleware/requestid"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	embeddingArk "github.com/cloudwego/eino-ext/components/embedding/ark"
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

type SearchApplication interface {
	Search(ctx context.Context, input ragapp.SearchInput) (ragapp.SearchOutput, error)
}

type RetrievalMetrics interface {
	RecordKnowledgeRetrieval(status string, mode string, duration time.Duration, resultCount int)
}

type Handler struct {
	application      Application
	search           SearchApplication
	searchInitError  error
	uploadMetrics    UploadMetrics
	retrievalMetrics RetrievalMetrics
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

type searchRequest struct {
	Query string `json:"query" binding:"required"`
	TopK  int    `json:"top_k,omitempty"`
}

type searchResponse struct {
	SchemaVersion string                   `json:"schema_version"`
	TraceID       string                   `json:"trace_id"`
	RequestID     string                   `json:"request_id"`
	Query         string                   `json:"query"`
	Hits          []ragapp.SearchHit       `json:"hits"`
	Diagnostics   ragapp.SearchDiagnostics `json:"diagnostics"`
}

func NewHandler(application Application, metrics UploadMetrics) *Handler {
	return &Handler{application: application, uploadMetrics: metrics}
}

func NewDefaultHandler() *Handler {
	repository := knowledgeapp.NewGormRepository(mysql.DB)
	metrics := observability.DefaultMetrics()
	handler := NewHandler(knowledgeapp.NewDefaultService(repository), metrics)
	handler.retrievalMetrics = metrics
	handler.search = new(lazyDefaultSearchApplication)
	return handler
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

func (handler *Handler) Search(context *gin.Context) {
	startedAt := time.Now()
	status := "error"
	mode := "unavailable"
	resultCount := 0
	defer func() {
		if handler.retrievalMetrics != nil {
			handler.retrievalMetrics.RecordKnowledgeRetrieval(status, mode, time.Since(startedAt), resultCount)
		}
	}()
	request := new(searchRequest)
	if err := context.ShouldBindJSON(request); err != nil {
		status = "rejected"
		handler.writeError(context, contract.NewDomainError("INVALID_KNOWLEDGE_SEARCH", contract.ErrorValidation, "请输入有效的检索问题", false, err))
		return
	}
	request.Query = strings.TrimSpace(request.Query)
	if handler.search == nil {
		handler.writeError(context, contract.NewDomainError("KNOWLEDGE_RETRIEVAL_UNAVAILABLE", contract.ErrorDependencyUnavailable, "知识检索暂时不可用", true, handler.searchInitError))
		return
	}
	userID := context.GetString("userName")
	output, err := handler.search.Search(context.Request.Context(), ragapp.SearchInput{
		TenantID: userID, UserID: userID, Query: request.Query, TopK: request.TopK,
	})
	if err != nil {
		if errors.Is(err, ragapp.ErrInvalidSearch) {
			status = "rejected"
			handler.writeError(context, contract.NewDomainError("INVALID_KNOWLEDGE_SEARCH", contract.ErrorValidation, "检索问题为空、过长或返回数量超出范围", false, err))
			return
		}
		handler.writeError(context, contract.NewDomainError("KNOWLEDGE_RETRIEVAL_UNAVAILABLE", contract.ErrorDependencyUnavailable, "知识检索暂时不可用", true, err))
		return
	}
	mode = output.Diagnostics.Mode
	resultCount = len(output.Hits)
	status = "success"
	if mode != "hybrid" {
		status = "degraded"
	}
	if resultCount == 0 {
		status = "empty"
	}
	requestID, traceID := requestid.IDs(context)
	context.Header(requestid.RequestIDHeader, requestID)
	context.Header(requestid.TraceIDHeader, traceID)
	context.JSON(http.StatusOK, searchResponse{
		SchemaVersion: contract.SchemaVersion, TraceID: traceID, RequestID: requestID, Query: request.Query,
		Hits: output.Hits, Diagnostics: output.Diagnostics,
	})
}

func (handler *Handler) writeError(context *gin.Context, err error) {
	_, traceID := requestid.IDs(context)
	domainError := contract.WithTrace(err, traceID)
	context.JSON(httpStatus(domainError.Category), domainError)
}

func (handler *Handler) recordUpload(status string, sizeBytes int64) {
	if handler.uploadMetrics != nil {
		handler.uploadMetrics.RecordDocumentUpload(status, sizeBytes)
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

func newDefaultSearchApplication() (SearchApplication, error) {
	configuration := config.GetConfig()
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return nil, errors.New("embedding API key is not configured")
	}
	timeout := 45 * time.Second
	retryTimes := 1
	embedder, err := embeddingArk.NewEmbedder(context.Background(), &embeddingArk.EmbeddingConfig{
		BaseURL: configuration.RagBaseUrl, APIKey: apiKey, Model: configuration.RagEmbeddingModel,
		Timeout: &timeout, RetryTimes: &retryTimes,
	})
	if err != nil {
		return nil, err
	}
	environment := strings.TrimSpace(os.Getenv("GOPHERAI_ENV"))
	return ragapp.NewHybridRetriever(
		ragapp.NewRedisSearchBackend(redisstore.Rdb), embedder,
		ragapp.NewGormAuthorityRepository(mysql.DB), environment, configuration.RagDimension,
	)
}

type lazyDefaultSearchApplication struct {
	once        sync.Once
	application SearchApplication
	err         error
}

func (application *lazyDefaultSearchApplication) Search(ctx context.Context, input ragapp.SearchInput) (ragapp.SearchOutput, error) {
	application.once.Do(func() {
		application.application, application.err = newDefaultSearchApplication()
	})
	if application.err != nil {
		return ragapp.SearchOutput{}, application.err
	}
	return application.application.Search(ctx, input)
}
