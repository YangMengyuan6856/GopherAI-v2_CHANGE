package knowledge

import (
	"GopherAI/internal/contract"
	"GopherAI/model"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	DefaultMaxUploadBytes  = int64(10 * 1024 * 1024)
	DocumentStatusUploaded = "uploaded"
	DocumentStatusParsing  = "parsing"
	DocumentStatusIndexed  = "indexed"
	DocumentStatusFailed   = "failed"
	DocumentStatusDeleted  = "deleted"
	JobStatusQueued        = "queued"
	JobTypeDocumentIndex   = "document_index"
	OutboxStatusPending    = "pending"
	DocumentIndexTopic     = "gopher.document.index.v1"
	DocumentIndexEventType = "document.index.requested"
	ParserVersionV1        = "plain-text-v1"
	ParserVersionDataV1    = "structured-data-v1"
	ParserVersionGoV1      = "go-ast-v1"
	ChunkerVersionV1       = "structure-token-v1"
	ChunkerVersionDataV1   = "key-path-token-v1"
	ChunkerVersionGoV1     = "go-symbol-token-v1"
)

type documentFormat struct {
	parserVersion  string
	chunkerVersion string
}

var allowedDocumentFormats = map[string]documentFormat{
	`.md`:   {parserVersion: ParserVersionV1, chunkerVersion: ChunkerVersionV1},
	`.txt`:  {parserVersion: ParserVersionV1, chunkerVersion: ChunkerVersionV1},
	`.json`: {parserVersion: ParserVersionDataV1, chunkerVersion: ChunkerVersionDataV1},
	`.yaml`: {parserVersion: ParserVersionDataV1, chunkerVersion: ChunkerVersionDataV1},
	`.yml`:  {parserVersion: ParserVersionDataV1, chunkerVersion: ChunkerVersionDataV1},
	`.go`:   {parserVersion: ParserVersionGoV1, chunkerVersion: ChunkerVersionGoV1},
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID() string
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type UUIDGenerator struct{}

func (UUIDGenerator) NewID() string { return uuid.NewString() }

type Service struct {
	repository  Repository
	storageRoot string
	maxBytes    int64
	clock       Clock
	ids         IDGenerator
}

type AcceptInput struct {
	TenantID string
	UserID   string
	TraceID  string
	File     *multipart.FileHeader
}

type AcceptResult struct {
	Document  DocumentSummary `json:"document"`
	Job       JobSummary      `json:"job"`
	Duplicate bool            `json:"duplicate"`
}

type DocumentSummary struct {
	ID             string    `json:"id"`
	DisplayName    string    `json:"display_name"`
	MimeType       string    `json:"mime_type"`
	CurrentVersion int       `json:"current_version"`
	Status         string    `json:"status"`
	SizeBytes      int64     `json:"size_bytes"`
	LastErrorCode  string    `json:"last_error_code,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type JobSummary struct {
	ID            string    `json:"id"`
	DocumentID    string    `json:"document_id"`
	Version       int       `json:"version"`
	JobType       string    `json:"job_type"`
	Status        string    `json:"status"`
	Attempt       int       `json:"attempt"`
	LastErrorCode string    `json:"last_error_code,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func NewService(repository Repository, storageRoot string, maxBytes int64, clock Clock, ids IDGenerator) (*Service, error) {
	if repository == nil || strings.TrimSpace(storageRoot) == "" || clock == nil || ids == nil {
		return nil, fmt.Errorf("repository, storage root, clock and id generator are required")
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("max upload bytes must be positive")
	}
	return &Service{repository: repository, storageRoot: storageRoot, maxBytes: maxBytes, clock: clock, ids: ids}, nil
}

func NewDefaultService(repository Repository) *Service {
	service, err := NewService(repository, filepath.Join("uploads", "knowledge"), DefaultMaxUploadBytes, SystemClock{}, UUIDGenerator{})
	if err != nil {
		panic(err)
	}
	return service
}

func (service *Service) Accept(ctx context.Context, input AcceptInput) (AcceptResult, error) {
	if strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.UserID) == "" || input.File == nil {
		return AcceptResult{}, contract.NewDomainError("INVALID_DOCUMENT_UPLOAD", contract.ErrorValidation, "文档上传参数不完整", false, nil)
	}
	displayName := safeDisplayName(input.File.Filename)
	extension := strings.ToLower(path.Ext(displayName))
	if displayName == "" || displayName == "." {
		return AcceptResult{}, contract.NewDomainError("DOCUMENT_NAME_INVALID", contract.ErrorValidation, "文档名称无效", false, nil)
	}
	format, allowed := allowedDocumentFormats[extension]
	if !allowed {
		return AcceptResult{}, contract.NewDomainError("DOCUMENT_TYPE_UNSUPPORTED", contract.ErrorValidation, "仅支持 .md、.txt、.json、.yaml、.yml 和 .go 文档", false, nil)
	}
	if input.File.Size > service.maxBytes {
		return AcceptResult{}, contract.NewDomainError("DOCUMENT_TOO_LARGE", contract.ErrorValidation, "文档不能超过 10 MB", false, nil)
	}

	documentID := service.ids.NewID()
	documentDirectory := filepath.Join(service.storageRoot, documentID)
	if err := os.MkdirAll(documentDirectory, 0o750); err != nil {
		return AcceptResult{}, dependencyError("DOCUMENT_STORAGE_UNAVAILABLE", "文档存储暂时不可用", err)
	}
	temporaryFile, err := os.CreateTemp(documentDirectory, ".upload-*")
	if err != nil {
		_ = os.RemoveAll(documentDirectory)
		return AcceptResult{}, dependencyError("DOCUMENT_STORAGE_UNAVAILABLE", "文档存储暂时不可用", err)
	}
	temporaryPath := temporaryFile.Name()
	removeTemporary := true
	defer func() {
		_ = temporaryFile.Close()
		if removeTemporary {
			_ = os.RemoveAll(documentDirectory)
		}
	}()

	source, err := input.File.Open()
	if err != nil {
		return AcceptResult{}, contract.NewDomainError("DOCUMENT_READ_FAILED", contract.ErrorValidation, "无法读取上传文档", false, err)
	}
	defer source.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(temporaryFile, hash), io.LimitReader(source, service.maxBytes+1))
	if err != nil {
		return AcceptResult{}, dependencyError("DOCUMENT_WRITE_FAILED", "文档保存失败", err)
	}
	if size > service.maxBytes {
		return AcceptResult{}, contract.NewDomainError("DOCUMENT_TOO_LARGE", contract.ErrorValidation, "文档不能超过 10 MB", false, nil)
	}
	if size == 0 {
		return AcceptResult{}, contract.NewDomainError("DOCUMENT_EMPTY", contract.ErrorValidation, "文档内容不能为空", false, nil)
	}
	if err := temporaryFile.Sync(); err != nil {
		return AcceptResult{}, dependencyError("DOCUMENT_WRITE_FAILED", "文档保存失败", err)
	}
	if _, err := temporaryFile.Seek(0, io.SeekStart); err != nil {
		return AcceptResult{}, dependencyError("DOCUMENT_READ_FAILED", "无法校验上传文档", err)
	}
	content, err := io.ReadAll(temporaryFile)
	if err != nil {
		return AcceptResult{}, dependencyError("DOCUMENT_READ_FAILED", "无法校验上传文档", err)
	}
	sniffSize := min(len(content), 512)
	detectedMime := strings.Split(http.DetectContentType(content[:sniffSize]), ";")[0]
	if (detectedMime != "text/plain" && detectedMime != "application/json") || !utf8.Valid(content) {
		return AcceptResult{}, contract.NewDomainError("DOCUMENT_CONTENT_INVALID", contract.ErrorValidation, "文档内容不是有效文本", false, nil)
	}
	contentHash := documentContentHash(format, hex.EncodeToString(hash.Sum(nil)))
	existingDocument, existingJob, err := service.repository.FindByContentHash(ctx, input.TenantID, contentHash)
	if err != nil {
		return AcceptResult{}, dependencyError("DOCUMENT_REPOSITORY_UNAVAILABLE", "文档服务暂时不可用", err)
	}
	if existingDocument != nil {
		return AcceptResult{Document: summarizeDocument(*existingDocument), Job: summarizeJob(existingJob), Duplicate: true}, nil
	}

	finalPath := filepath.Join(documentDirectory, fmt.Sprintf("v1%s", extension))
	if err := temporaryFile.Close(); err != nil {
		return AcceptResult{}, dependencyError("DOCUMENT_WRITE_FAILED", "文档保存失败", err)
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return AcceptResult{}, dependencyError("DOCUMENT_WRITE_FAILED", "文档保存失败", err)
	}
	removeTemporary = false

	now := service.clock.Now()
	versionID := service.ids.NewID()
	jobID := service.ids.NewID()
	eventID := service.ids.NewID()
	document := &model.KnowledgeDocument{
		ID: documentID, TenantID: input.TenantID, UserID: input.UserID, DisplayName: displayName,
		MimeType: detectedMime, CurrentVersion: 1, Status: DocumentStatusUploaded,
		SizeBytes: size, ContentHash: contentHash, StoragePath: finalPath, CreatedAt: now, UpdatedAt: now,
	}
	version := &model.KnowledgeDocumentVersion{
		ID: versionID, DocumentID: documentID, Version: 1, Status: DocumentStatusUploaded,
		ContentHash: contentHash, StoragePath: finalPath, ParserVersion: format.parserVersion,
		ChunkerVersion: format.chunkerVersion, CreatedAt: now, UpdatedAt: now,
	}
	job := &model.KnowledgeJob{
		ID: jobID, TenantID: input.TenantID, DocumentID: documentID, Version: 1,
		JobType: JobTypeDocumentIndex, Status: JobStatusQueued, CreatedAt: now, UpdatedAt: now,
	}
	payload, _ := json.Marshal(map[string]any{"job_id": jobID, "document_id": documentID, "version": 1})
	event := &model.OutboxEvent{
		ID: eventID, Topic: DocumentIndexTopic, EventType: DocumentIndexEventType,
		TraceID: input.TraceID, TenantID: input.TenantID, AggregateID: documentID,
		AggregateVersion: 1, PayloadJSON: string(payload), Status: OutboxStatusPending,
		AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := service.repository.CreateUpload(ctx, document, version, job, event); err != nil {
		_ = os.RemoveAll(documentDirectory)
		existingDocument, existingJob, lookupErr := service.repository.FindByContentHash(ctx, input.TenantID, contentHash)
		if lookupErr == nil && existingDocument != nil {
			return AcceptResult{Document: summarizeDocument(*existingDocument), Job: summarizeJob(existingJob), Duplicate: true}, nil
		}
		return AcceptResult{}, dependencyError("DOCUMENT_REPOSITORY_UNAVAILABLE", "文档服务暂时不可用", err)
	}
	return AcceptResult{Document: summarizeDocument(*document), Job: summarizeJob(job)}, nil
}

func documentContentHash(format documentFormat, rawContentHash string) string {
	// Preserve the original Markdown/TXT identity so existing uploads remain
	// idempotent after this release. New parser families domain-separate their
	// identity because identical bytes parsed as plain text, structured data,
	// or Go source produce different canonical chunks.
	if format.parserVersion == ParserVersionV1 && format.chunkerVersion == ChunkerVersionV1 {
		return rawContentHash
	}
	digest := sha256.Sum256([]byte(format.parserVersion + "\x00" + format.chunkerVersion + "\x00" + rawContentHash))
	return hex.EncodeToString(digest[:])
}

func (service *Service) List(ctx context.Context, tenantID string) ([]DocumentSummary, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, contract.NewDomainError("TENANT_REQUIRED", contract.ErrorAuth, "登录状态无效", false, nil)
	}
	documents, err := service.repository.ListDocuments(ctx, tenantID)
	if err != nil {
		return nil, dependencyError("DOCUMENT_REPOSITORY_UNAVAILABLE", "文档服务暂时不可用", err)
	}
	result := make([]DocumentSummary, 0, len(documents))
	for _, document := range documents {
		result = append(result, summarizeDocument(document))
	}
	return result, nil
}

func (service *Service) Job(ctx context.Context, tenantID string, jobID string) (JobSummary, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(jobID) == "" {
		return JobSummary{}, contract.NewDomainError("INVALID_JOB_REQUEST", contract.ErrorValidation, "任务查询参数错误", false, nil)
	}
	job, err := service.repository.GetJob(ctx, tenantID, jobID)
	if err != nil {
		return JobSummary{}, dependencyError("DOCUMENT_REPOSITORY_UNAVAILABLE", "文档服务暂时不可用", err)
	}
	if job == nil {
		return JobSummary{}, contract.NewDomainError("KNOWLEDGE_JOB_NOT_FOUND", contract.ErrorNotFound, "索引任务不存在", false, nil)
	}
	return summarizeJob(job), nil
}

func summarizeDocument(document model.KnowledgeDocument) DocumentSummary {
	return DocumentSummary{
		ID: document.ID, DisplayName: document.DisplayName, MimeType: document.MimeType,
		CurrentVersion: document.CurrentVersion, Status: document.Status, SizeBytes: document.SizeBytes,
		LastErrorCode: document.LastErrorCode, CreatedAt: document.CreatedAt, UpdatedAt: document.UpdatedAt,
	}
}

func summarizeJob(job *model.KnowledgeJob) JobSummary {
	if job == nil {
		return JobSummary{}
	}
	return JobSummary{
		ID: job.ID, DocumentID: job.DocumentID, Version: job.Version, JobType: job.JobType,
		Status: job.Status, Attempt: job.Attempt, LastErrorCode: job.LastErrorCode,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
	}
}

func safeDisplayName(original string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(original), `\`, "/")
	return path.Base(normalized)
}

func dependencyError(code string, message string, cause error) *contract.DomainError {
	return contract.NewDomainError(code, contract.ErrorDependencyUnavailable, message, true, cause)
}
