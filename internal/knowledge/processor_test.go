package knowledge

import (
	"GopherAI/internal/jobqueue"
	"GopherAI/model"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type processorRepository struct {
	work                IndexWork
	deleteWork          DeleteWork
	claimError          error
	chunks              []model.KnowledgeChunk
	completeCalls       int
	retryCode           string
	failureCode         string
	replaceCalls        int
	replaceError        error
	completionError     error
	deleteCompleteCalls int
	deleteRetryCode     string
	deleteFailureCode   string
}

func (repository *processorRepository) ClaimIndexJob(context.Context, string, string, string, int) (IndexWork, error) {
	return repository.work, repository.claimError
}

func (repository *processorRepository) ReplaceChunks(_ context.Context, _ string, _ int, chunks []model.KnowledgeChunk) error {
	repository.replaceCalls++
	repository.chunks = append([]model.KnowledgeChunk(nil), chunks...)
	return repository.replaceError
}

func (repository *processorRepository) CompleteIndex(context.Context, IndexWork, string, time.Time) error {
	repository.completeCalls++
	return repository.completionError
}

func (repository *processorRepository) RecordIndexRetry(_ context.Context, _ IndexWork, code string) error {
	repository.retryCode = code
	return nil
}

func (repository *processorRepository) FailIndex(_ context.Context, _ IndexWork, code string) error {
	repository.failureCode = code
	return nil
}

func (repository *processorRepository) FailIndexByIdentity(_ context.Context, _ string, _ string, _ string, _ int, code string) error {
	repository.failureCode = code
	return nil
}

func (repository *processorRepository) ClaimDeleteJob(context.Context, string, string, string, int) (DeleteWork, error) {
	return repository.deleteWork, repository.claimError
}

func (repository *processorRepository) CompleteDelete(context.Context, DeleteWork) error {
	repository.deleteCompleteCalls++
	return repository.completionError
}

func (repository *processorRepository) RecordDeleteRetry(_ context.Context, _ DeleteWork, code string) error {
	repository.deleteRetryCode = code
	return nil
}

func (repository *processorRepository) FailDeleteByIdentity(_ context.Context, _ string, _ string, _ string, _ int, code string) error {
	repository.deleteFailureCode = code
	return nil
}

type processorIndexer struct {
	chunks     []model.KnowledgeChunk
	deletedIDs []string
	err        error
}

func (indexer *processorIndexer) Index(_ context.Context, chunks []model.KnowledgeChunk) error {
	indexer.chunks = append([]model.KnowledgeChunk(nil), chunks...)
	return indexer.err
}

func (indexer *processorIndexer) Delete(_ context.Context, chunkIDs []string) error {
	indexer.deletedIDs = append([]string(nil), chunkIDs...)
	return indexer.err
}

func TestProcessorBuildsCanonicalChunksAndCompletes(t *testing.T) {
	storagePath := filepath.Join(t.TempDir(), "v1.md")
	if err := os.WriteFile(storagePath, []byte("# Config\nDefault retry: 7\n\n## Health\nUse /health/ready.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repository := &processorRepository{work: indexWorkFixture(storagePath)}
	indexer := new(processorIndexer)
	processor, err := NewProcessor(repository, NewDefaultStructuredTextChunker(), indexer, "embedding-v1", fixedClock{value: time.Date(2026, 9, 4, 2, 3, 4, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}

	if err := processor.Process(context.Background(), indexEnvelope(t)); err != nil {
		t.Fatal(err)
	}
	if repository.replaceCalls != 1 || repository.completeCalls != 1 || len(repository.chunks) == 0 || len(indexer.chunks) != len(repository.chunks) {
		t.Fatalf("unexpected processing calls: replace=%d complete=%d chunks=%d indexed=%d", repository.replaceCalls, repository.completeCalls, len(repository.chunks), len(indexer.chunks))
	}
	for index, chunk := range repository.chunks {
		if chunk.ID == "" || chunk.Ordinal != index || chunk.TenantID != "tenant-a" || chunk.DocumentVersion != 1 || chunk.IndexStatus != ChunkIndexStatusPending {
			t.Fatalf("invalid canonical chunk: %+v", chunk)
		}
		if deterministicChunkID("document-1", 1, ChunkDraft{Ordinal: chunk.Ordinal, ContentHash: chunk.ContentHash}) != chunk.ID {
			t.Fatalf("chunk ID is not deterministic: %+v", chunk)
		}
	}
}

func TestProcessorPersistsStructuredKeyPathMetadata(t *testing.T) {
	storagePath := filepath.Join(t.TempDir(), "v1.yaml")
	if err := os.WriteFile(storagePath, []byte("service:\n  retry:\n    max_attempts: 7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repository := &processorRepository{work: indexWorkFixture(storagePath)}
	indexer := new(processorIndexer)
	processor, err := NewProcessor(repository, NewDefaultStructuredTextChunker(), indexer, "embedding-v1", fixedClock{value: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(context.Background(), indexEnvelope(t)); err != nil {
		t.Fatal(err)
	}
	if len(repository.chunks) != 1 {
		t.Fatalf("expected one structured chunk, got %+v", repository.chunks)
	}
	chunk := repository.chunks[0]
	if chunk.SectionPath != "service > retry > max_attempts" || chunk.LineStart != 3 || chunk.LineEnd != 3 {
		t.Fatalf("structured metadata was not persisted: %+v", chunk)
	}
	metadata := make(map[string]any)
	if err := json.Unmarshal([]byte(chunk.MetadataJSON), &metadata); err != nil || metadata["section_path"] != "service > retry > max_attempts" {
		t.Fatalf("structured metadata JSON is incomplete: %s err=%v", chunk.MetadataJSON, err)
	}
}

func TestProcessorMarksInvalidStructuredDocumentAsNonRetryable(t *testing.T) {
	storagePath := filepath.Join(t.TempDir(), "v1.json")
	if err := os.WriteFile(storagePath, []byte(`{"retry": }`), 0o600); err != nil {
		t.Fatal(err)
	}
	repository := &processorRepository{work: indexWorkFixture(storagePath)}
	processor, err := NewProcessor(repository, NewDefaultStructuredTextChunker(), new(processorIndexer), "embedding-v1", fixedClock{value: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	err = processor.Process(context.Background(), indexEnvelope(t))
	var processingError *ProcessingError
	if !errors.As(err, &processingError) || processingError.Retryable || processingError.Code != ErrorCodeParseFailed {
		t.Fatalf("unexpected structured parse failure: %v", err)
	}
	if repository.failureCode != ErrorCodeParseFailed || repository.replaceCalls != 0 || repository.completeCalls != 0 {
		t.Fatalf("invalid syntax must fail before persistence/indexing: %+v", repository)
	}
}

func TestProcessorUsesCandidateVersionFormatInsteadOfActiveDisplayName(t *testing.T) {
	storagePath := filepath.Join(t.TempDir(), "candidate.json")
	if err := os.WriteFile(storagePath, []byte(`{"retry": }`), 0o600); err != nil {
		t.Fatal(err)
	}
	work := indexWorkFixture(storagePath)
	work.Document.DisplayName = "active.md"
	work.Version.DisplayName = "candidate.json"
	repository := &processorRepository{work: work}
	processor, err := NewProcessor(repository, NewDefaultStructuredTextChunker(), new(processorIndexer), "embedding-v1", fixedClock{value: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	err = processor.Process(context.Background(), indexEnvelope(t))
	var processingError *ProcessingError
	if !errors.As(err, &processingError) || processingError.Code != ErrorCodeParseFailed || processingError.Retryable {
		t.Fatalf("candidate extension must choose structured parser, got %v", err)
	}
}

func TestProcessorAcknowledgesAlreadyCompletedJobWithoutSideEffects(t *testing.T) {
	repository := &processorRepository{work: IndexWork{Complete: true}}
	indexer := new(processorIndexer)
	processor, err := NewProcessor(repository, NewDefaultStructuredTextChunker(), indexer, "embedding-v1", fixedClock{value: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(context.Background(), indexEnvelope(t)); err != nil {
		t.Fatal(err)
	}
	if repository.replaceCalls != 0 || repository.completeCalls != 0 || len(indexer.chunks) != 0 {
		t.Fatal("completed redelivery must be a no-op")
	}
}

func TestProcessorClassifiesIndexerFailureAsRetryable(t *testing.T) {
	storagePath := filepath.Join(t.TempDir(), "v1.txt")
	if err := os.WriteFile(storagePath, []byte("retry this index operation"), 0o600); err != nil {
		t.Fatal(err)
	}
	repository := &processorRepository{work: indexWorkFixture(storagePath)}
	indexer := &processorIndexer{err: errors.New("redis unavailable")}
	processor, err := NewProcessor(repository, NewDefaultStructuredTextChunker(), indexer, "embedding-v1", fixedClock{value: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	err = processor.Process(context.Background(), indexEnvelope(t))
	var processingError *ProcessingError
	if !errors.As(err, &processingError) || !processingError.Retryable || processingError.Code != ErrorCodeRedisIndex {
		t.Fatalf("unexpected processing error: %v", err)
	}
	if repository.retryCode != ErrorCodeRedisIndex || repository.completeCalls != 0 {
		t.Fatalf("retry state was not recorded: %+v", repository)
	}
}

func TestProcessorRejectsMalformedEventWithoutClaimingJob(t *testing.T) {
	repository := new(processorRepository)
	processor, err := NewProcessor(repository, NewDefaultStructuredTextChunker(), new(processorIndexer), "embedding-v1", fixedClock{value: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	err = processor.Process(context.Background(), jobqueue.Envelope{EventType: "unknown"})
	var processingError *ProcessingError
	if !errors.As(err, &processingError) || processingError.Retryable || processingError.Code != ErrorCodeEventInvalid {
		t.Fatalf("unexpected processing error: %v", err)
	}
}

func TestProcessorDeletesRedisChunksBeforeCompletingAuthorityCleanup(t *testing.T) {
	repository := &processorRepository{deleteWork: DeleteWork{
		Document: model.KnowledgeDocument{ID: "document-1", TenantID: "tenant-a", Status: DocumentStatusDeleted},
		Job:      model.KnowledgeJob{ID: "delete-job", TenantID: "tenant-a", DocumentID: "document-1", Version: 1, JobType: JobTypeDocumentDelete},
		ChunkIDs: []string{"chunk-v1", "chunk-v2"},
	}}
	indexer := new(processorIndexer)
	processor, err := NewProcessor(repository, NewDefaultStructuredTextChunker(), indexer, "embedding-v1", fixedClock{value: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(context.Background(), deleteEnvelope(t)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(indexer.deletedIDs, []string{"chunk-v1", "chunk-v2"}) || repository.deleteCompleteCalls != 1 {
		t.Fatalf("delete cleanup order was not completed: ids=%v repository=%+v", indexer.deletedIDs, repository)
	}
}

func TestProcessorRetriesDeleteWhenRedisIsUnavailable(t *testing.T) {
	repository := &processorRepository{deleteWork: DeleteWork{ChunkIDs: []string{"chunk-v1"}}}
	indexer := &processorIndexer{err: errors.New("redis unavailable")}
	processor, err := NewProcessor(repository, NewDefaultStructuredTextChunker(), indexer, "embedding-v1", fixedClock{value: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	err = processor.Process(context.Background(), deleteEnvelope(t))
	var processingError *ProcessingError
	if !errors.As(err, &processingError) || processingError.Code != ErrorCodeRedisDelete || !processingError.Retryable {
		t.Fatalf("unexpected delete failure: %v", err)
	}
	if repository.deleteRetryCode != ErrorCodeRedisDelete || repository.deleteCompleteCalls != 0 {
		t.Fatalf("delete failure must remain retryable: %+v", repository)
	}
}

func TestProcessorExhaustsDeleteIntoStableFailedJob(t *testing.T) {
	repository := new(processorRepository)
	processor, err := NewProcessor(repository, NewDefaultStructuredTextChunker(), new(processorIndexer), "embedding-v1", fixedClock{value: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Exhaust(context.Background(), deleteEnvelope(t), ErrorCodeRedisDelete); err != nil {
		t.Fatal(err)
	}
	if repository.deleteFailureCode != ErrorCodeRedisDelete {
		t.Fatalf("delete exhaustion did not persist stable code: %+v", repository)
	}
}

func TestVersionAliasMovesOnlyOnSuccessfulCompletion(t *testing.T) {
	document := model.KnowledgeDocument{
		ID: "document-1", CurrentVersion: 1, Status: DocumentStatusIndexed,
		DisplayName: "runbook-v1.md", MimeType: "text/plain", SizeBytes: 10,
		ContentHash: "old-hash", StoragePath: "/old",
	}
	work := IndexWork{
		Document: document,
		Version: model.KnowledgeDocumentVersion{
			Version: 2, DisplayName: "runbook.md", MimeType: "text/plain", SizeBytes: 20,
			ContentHash: "new-hash", StoragePath: "/new",
		},
	}
	completion := documentCompletionUpdates(work)
	if completion["current_version"] != 2 || completion["content_hash"] != "new-hash" || completion["storage_path"] != "/new" {
		t.Fatalf("successful completion must atomically select the candidate: %+v", completion)
	}
	failure := documentFailureUpdates(document, 2, ErrorCodeParseFailed)
	if _, changesStatus := failure["status"]; changesStatus || failure["last_error_code"] != ErrorCodeParseFailed {
		t.Fatalf("failed candidate must preserve indexed active version: %+v", failure)
	}
	initialFailure := documentFailureUpdates(model.KnowledgeDocument{CurrentVersion: 1, Status: DocumentStatusParsing}, 1, ErrorCodeParseFailed)
	if initialFailure["status"] != DocumentStatusFailed {
		t.Fatalf("initial version failure must still fail the document: %+v", initialFailure)
	}
	staleFailure := documentFailureUpdates(model.KnowledgeDocument{CurrentVersion: 3, Status: DocumentStatusIndexed}, 2, ErrorCodeRedisIndex)
	if len(staleFailure) != 0 {
		t.Fatalf("late failure from an older candidate must not contaminate the active version: %+v", staleFailure)
	}
}

func indexWorkFixture(storagePath string) IndexWork {
	return IndexWork{
		Document: model.KnowledgeDocument{ID: "document-1", TenantID: "tenant-a", UserID: "user-a", DisplayName: filepath.Base(storagePath)},
		Version:  model.KnowledgeDocumentVersion{ID: "version-1", DocumentID: "document-1", Version: 1, StoragePath: storagePath},
		Job:      model.KnowledgeJob{ID: "job-1", TenantID: "tenant-a", DocumentID: "document-1", Version: 1},
	}
}

func indexEnvelope(t *testing.T) jobqueue.Envelope {
	t.Helper()
	payload, err := json.Marshal(indexPayload{JobID: "job-1", DocumentID: "document-1", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	return jobqueue.Envelope{
		SchemaVersion: "1", EventID: "event-1", EventType: DocumentIndexEventType,
		TenantID: "tenant-a", AggregateID: "document-1", AggregateVersion: 1, Payload: payload,
	}
}

func deleteEnvelope(t *testing.T) jobqueue.Envelope {
	t.Helper()
	payload, err := json.Marshal(indexPayload{JobID: "delete-job", DocumentID: "document-1", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	return jobqueue.Envelope{
		SchemaVersion: "1", EventID: "delete-event", EventType: DocumentDeleteEventType,
		TenantID: "tenant-a", AggregateID: "document-1", AggregateVersion: 1, Payload: payload,
	}
}
