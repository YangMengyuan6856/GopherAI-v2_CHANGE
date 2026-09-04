package knowledge

import (
	"GopherAI/internal/jobqueue"
	"GopherAI/model"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type processorRepository struct {
	work            IndexWork
	claimError      error
	chunks          []model.KnowledgeChunk
	completeCalls   int
	retryCode       string
	failureCode     string
	replaceCalls    int
	replaceError    error
	completionError error
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

type processorIndexer struct {
	chunks []model.KnowledgeChunk
	err    error
}

func (indexer *processorIndexer) Index(_ context.Context, chunks []model.KnowledgeChunk) error {
	indexer.chunks = append([]model.KnowledgeChunk(nil), chunks...)
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
