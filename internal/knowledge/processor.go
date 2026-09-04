package knowledge

import (
	"GopherAI/internal/jobqueue"
	"GopherAI/model"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"
)

const (
	JobStatusProcessing       = "processing"
	JobStatusRetrying         = "retrying"
	JobStatusCompleted        = "completed"
	JobStatusFailed           = "failed"
	ChunkIndexStatusPending   = "pending"
	ChunkIndexStatusIndexed   = "indexed"
	ChunkIndexStatusFailed    = "failed"
	ErrorCodeEventInvalid     = "INDEX_EVENT_INVALID"
	ErrorCodeJobNotFound      = "INDEX_JOB_NOT_FOUND"
	ErrorCodeStorageRead      = "DOCUMENT_STORAGE_READ_FAILED"
	ErrorCodeParseFailed      = "DOCUMENT_PARSE_FAILED"
	ErrorCodeChunkPersist     = "CHUNK_PERSIST_FAILED"
	ErrorCodeRedisIndex       = "REDIS_INDEX_FAILED"
	ErrorCodeIndexCompletion  = "INDEX_COMPLETION_FAILED"
	ErrorCodeRedisDelete      = "REDIS_DELETE_FAILED"
	ErrorCodeDeleteCompletion = "DELETE_COMPLETION_FAILED"
)

type ChunkIndexer interface {
	Index(ctx context.Context, chunks []model.KnowledgeChunk) error
	Delete(ctx context.Context, chunkIDs []string) error
}

type Processor struct {
	repository       IndexRepository
	parserChunker    ParserChunker
	indexer          ChunkIndexer
	embeddingVersion string
	clock            Clock
}

type indexPayload struct {
	JobID      string `json:"job_id"`
	DocumentID string `json:"document_id"`
	Version    int    `json:"version"`
}

type ProcessingError struct {
	Code      string
	Retryable bool
	Cause     error
}

func (processingError *ProcessingError) Error() string {
	if processingError == nil {
		return ""
	}
	return processingError.Code
}

func (processingError *ProcessingError) Unwrap() error {
	if processingError == nil {
		return nil
	}
	return processingError.Cause
}

func NewProcessor(repository IndexRepository, parserChunker ParserChunker, indexer ChunkIndexer, embeddingVersion string, clock Clock) (*Processor, error) {
	if repository == nil || parserChunker == nil || indexer == nil || embeddingVersion == "" || clock == nil {
		return nil, fmt.Errorf("repository, parser, indexer, embedding version and clock are required")
	}
	return &Processor{repository: repository, parserChunker: parserChunker, indexer: indexer, embeddingVersion: embeddingVersion, clock: clock}, nil
}

func (processor *Processor) Process(ctx context.Context, envelope jobqueue.Envelope) error {
	if envelope.TenantID == "" || envelope.AggregateID == "" || envelope.AggregateVersion <= 0 {
		return processError(ErrorCodeEventInvalid, false, nil)
	}
	switch envelope.EventType {
	case DocumentIndexEventType:
		return processor.processIndex(ctx, envelope)
	case DocumentDeleteEventType:
		return processor.processDelete(ctx, envelope)
	default:
		return processError(ErrorCodeEventInvalid, false, nil)
	}
}

func (processor *Processor) processIndex(ctx context.Context, envelope jobqueue.Envelope) error {
	payload := new(indexPayload)
	if err := json.Unmarshal(envelope.Payload, payload); err != nil || payload.JobID == "" || payload.DocumentID != envelope.AggregateID || payload.Version != envelope.AggregateVersion {
		return processError(ErrorCodeEventInvalid, false, err)
	}
	work, err := processor.repository.ClaimIndexJob(ctx, envelope.TenantID, payload.JobID, payload.DocumentID, payload.Version)
	if err != nil {
		if isRecordNotFound(err) {
			return processError(ErrorCodeJobNotFound, false, err)
		}
		return processError(ErrorCodeJobNotFound, true, err)
	}
	if work.Complete {
		return nil
	}
	content, err := os.ReadFile(work.Version.StoragePath)
	if err != nil {
		return processor.fail(ctx, work, ErrorCodeStorageRead, true, err)
	}
	sourceName := work.Version.DisplayName
	if sourceName == "" {
		sourceName = work.Document.DisplayName
	}
	drafts, err := processor.parserChunker.ParseAndChunk(sourceName, content)
	if err != nil {
		return processor.fail(ctx, work, ErrorCodeParseFailed, false, err)
	}
	chunks := make([]model.KnowledgeChunk, 0, len(drafts))
	now := processor.clock.Now()
	for _, draft := range drafts {
		metadata, _ := json.Marshal(map[string]any{"document_id": work.Document.ID, "version": work.Version.Version, "section_path": draft.SectionPath, "line_start": draft.LineStart, "line_end": draft.LineEnd})
		chunks = append(chunks, model.KnowledgeChunk{
			ID:         deterministicChunkID(work.Document.ID, work.Version.Version, draft),
			DocumentID: work.Document.ID, DocumentVersion: work.Version.Version,
			TenantID: work.Document.TenantID, UserID: work.Document.UserID,
			Ordinal: draft.Ordinal, SectionPath: draft.SectionPath, LineStart: draft.LineStart, LineEnd: draft.LineEnd,
			Content: draft.Content, TokenCount: draft.TokenCount, ContentHash: draft.ContentHash,
			MetadataJSON: string(metadata), EmbeddingVersion: processor.embeddingVersion,
			IndexStatus: ChunkIndexStatusPending, CreatedAt: now, UpdatedAt: now,
		})
	}
	if err := processor.repository.ReplaceChunks(ctx, work.Document.ID, work.Version.Version, chunks); err != nil {
		return processor.fail(ctx, work, ErrorCodeChunkPersist, true, err)
	}
	if err := processor.indexer.Index(ctx, chunks); err != nil {
		return processor.fail(ctx, work, ErrorCodeRedisIndex, true, err)
	}
	if err := processor.repository.CompleteIndex(ctx, work, processor.embeddingVersion, processor.clock.Now()); err != nil {
		return processor.fail(ctx, work, ErrorCodeIndexCompletion, true, err)
	}
	return nil
}

func (processor *Processor) processDelete(ctx context.Context, envelope jobqueue.Envelope) error {
	payload := new(indexPayload)
	if err := json.Unmarshal(envelope.Payload, payload); err != nil || payload.JobID == "" || payload.DocumentID != envelope.AggregateID || payload.Version != envelope.AggregateVersion {
		return processError(ErrorCodeEventInvalid, false, err)
	}
	work, err := processor.repository.ClaimDeleteJob(ctx, envelope.TenantID, payload.JobID, payload.DocumentID, payload.Version)
	if err != nil {
		if isRecordNotFound(err) {
			return processError(ErrorCodeJobNotFound, false, err)
		}
		return processError(ErrorCodeJobNotFound, true, err)
	}
	if work.Complete {
		return nil
	}
	if err := processor.indexer.Delete(ctx, work.ChunkIDs); err != nil {
		_ = processor.repository.RecordDeleteRetry(ctx, work, ErrorCodeRedisDelete)
		return processError(ErrorCodeRedisDelete, true, err)
	}
	if err := processor.repository.CompleteDelete(ctx, work); err != nil {
		_ = processor.repository.RecordDeleteRetry(ctx, work, ErrorCodeDeleteCompletion)
		return processError(ErrorCodeDeleteCompletion, true, err)
	}
	return nil
}

func (processor *Processor) Exhaust(ctx context.Context, envelope jobqueue.Envelope, code string) error {
	payload := new(indexPayload)
	if err := json.Unmarshal(envelope.Payload, payload); err != nil || payload.JobID == "" || payload.DocumentID != envelope.AggregateID || payload.Version != envelope.AggregateVersion {
		return processError(ErrorCodeEventInvalid, false, err)
	}
	switch envelope.EventType {
	case DocumentIndexEventType:
		return processor.repository.FailIndexByIdentity(ctx, envelope.TenantID, payload.JobID, payload.DocumentID, payload.Version, code)
	case DocumentDeleteEventType:
		return processor.repository.FailDeleteByIdentity(ctx, envelope.TenantID, payload.JobID, payload.DocumentID, payload.Version, code)
	default:
		return processError(ErrorCodeEventInvalid, false, nil)
	}
}

func (processor *Processor) fail(ctx context.Context, work IndexWork, code string, retryable bool, cause error) error {
	var stateError error
	if retryable {
		stateError = processor.repository.RecordIndexRetry(ctx, work, code)
	} else {
		stateError = processor.repository.FailIndex(ctx, work, code)
	}
	if stateError != nil {
		cause = errors.Join(cause, stateError)
	}
	return processError(code, retryable, cause)
}

func processError(code string, retryable bool, cause error) *ProcessingError {
	return &ProcessingError{Code: code, Retryable: retryable, Cause: cause}
}

func deterministicChunkID(documentID string, version int, draft ChunkDraft) string {
	seed := fmt.Sprintf("%s|%d|%d|%s", documentID, version, draft.Ordinal, draft.ContentHash)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed)).String()
}
