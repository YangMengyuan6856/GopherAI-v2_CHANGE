package knowledge

import (
	"GopherAI/internal/contract"
	"GopherAI/model"
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixedClock struct{ value time.Time }

func (clock fixedClock) Now() time.Time { return clock.value }

type sequenceIDs struct {
	next int
}

func (ids *sequenceIDs) NewID() string {
	ids.next++
	return "id-" + string(rune('a'+ids.next-1))
}

type memoryRepository struct {
	documents []model.KnowledgeDocument
	versions  []model.KnowledgeDocumentVersion
	jobs      []model.KnowledgeJob
	events    []model.OutboxEvent
}

func (repository *memoryRepository) FindByContentHash(_ context.Context, tenantID string, contentHash string) (*model.KnowledgeDocument, *model.KnowledgeJob, error) {
	for index := range repository.documents {
		document := &repository.documents[index]
		if document.TenantID != tenantID || document.ContentHash != contentHash || document.Status == DocumentStatusDeleted {
			continue
		}
		for jobIndex := range repository.jobs {
			if repository.jobs[jobIndex].TenantID == tenantID && repository.jobs[jobIndex].DocumentID == document.ID {
				return document, &repository.jobs[jobIndex], nil
			}
		}
		return document, nil, nil
	}
	return nil, nil, nil
}

func (repository *memoryRepository) CreateUpload(_ context.Context, document *model.KnowledgeDocument, version *model.KnowledgeDocumentVersion, job *model.KnowledgeJob, event *model.OutboxEvent) error {
	repository.documents = append(repository.documents, *document)
	repository.versions = append(repository.versions, *version)
	repository.jobs = append(repository.jobs, *job)
	repository.events = append(repository.events, *event)
	return nil
}

func (repository *memoryRepository) ListDocuments(_ context.Context, tenantID string) ([]model.KnowledgeDocument, error) {
	result := make([]model.KnowledgeDocument, 0)
	for _, document := range repository.documents {
		if document.TenantID == tenantID && document.Status != DocumentStatusDeleted {
			result = append(result, document)
		}
	}
	return result, nil
}

func (repository *memoryRepository) GetJob(_ context.Context, tenantID string, jobID string) (*model.KnowledgeJob, error) {
	for index := range repository.jobs {
		if repository.jobs[index].TenantID == tenantID && repository.jobs[index].ID == jobID {
			return &repository.jobs[index], nil
		}
	}
	return nil, nil
}

func TestAcceptPersistsMultipleDocumentsAndOutbox(t *testing.T) {
	repository := new(memoryRepository)
	storageRoot := t.TempDir()
	clock := fixedClock{value: time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)}
	service, err := NewService(repository, storageRoot, DefaultMaxUploadBytes, clock, new(sequenceIDs))
	if err != nil {
		t.Fatal(err)
	}

	first, err := service.Accept(context.Background(), AcceptInput{
		TenantID: "tenant-a", UserID: "user-a", TraceID: "trace-a",
		File: multipartFile(t, `..\unsafe\project.md`, []byte("# Project\nRetry count: 7\n")),
	})
	if err != nil {
		t.Fatalf("accept first document: %v", err)
	}
	second, err := service.Accept(context.Background(), AcceptInput{
		TenantID: "tenant-a", UserID: "user-a", TraceID: "trace-b",
		File: multipartFile(t, "runbook.txt", []byte("restart with a bounded timeout\n")),
	})
	if err != nil {
		t.Fatalf("accept second document: %v", err)
	}

	if first.Document.DisplayName != "project.md" || first.Document.Status != DocumentStatusUploaded || first.Job.Status != JobStatusQueued {
		t.Fatalf("unexpected first result: %+v", first)
	}
	if second.Document.ID == first.Document.ID {
		t.Fatal("distinct documents must receive distinct IDs")
	}
	if len(repository.documents) != 2 || len(repository.versions) != 2 || len(repository.jobs) != 2 || len(repository.events) != 2 {
		t.Fatalf("expected two complete persistence groups, got documents=%d versions=%d jobs=%d events=%d", len(repository.documents), len(repository.versions), len(repository.jobs), len(repository.events))
	}
	if repository.events[0].TraceID != "trace-a" || repository.events[0].Status != OutboxStatusPending || !strings.Contains(repository.events[0].PayloadJSON, first.Job.ID) {
		t.Fatalf("unexpected outbox event: %+v", repository.events[0])
	}
	if strings.Contains(repository.documents[0].StoragePath, "tenant-a") || strings.Contains(repository.documents[0].StoragePath, "user-a") {
		t.Fatalf("storage path must not derive from tenant or user input: %s", repository.documents[0].StoragePath)
	}
	content, err := os.ReadFile(repository.documents[0].StoragePath)
	if err != nil || string(content) != "# Project\nRetry count: 7\n" {
		t.Fatalf("stored content mismatch: %q, %v", content, err)
	}
	documents, err := service.List(context.Background(), "tenant-a")
	if err != nil || len(documents) != 2 {
		t.Fatalf("expected both documents to remain visible, got %d, %v", len(documents), err)
	}
}

func TestAcceptIsIdempotentPerTenantAndContent(t *testing.T) {
	repository := new(memoryRepository)
	storageRoot := t.TempDir()
	service, err := NewService(repository, storageRoot, DefaultMaxUploadBytes, fixedClock{value: time.Now().UTC()}, new(sequenceIDs))
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("same content\n")
	first, err := service.Accept(context.Background(), AcceptInput{TenantID: "tenant-a", UserID: "user-a", TraceID: "trace-a", File: multipartFile(t, "a.md", content)})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.Accept(context.Background(), AcceptInput{TenantID: "tenant-a", UserID: "user-a", TraceID: "trace-b", File: multipartFile(t, "renamed.txt", content)})
	if err != nil {
		t.Fatal(err)
	}
	otherTenant, err := service.Accept(context.Background(), AcceptInput{TenantID: "tenant-b", UserID: "user-b", TraceID: "trace-c", File: multipartFile(t, "same.md", content)})
	if err != nil {
		t.Fatal(err)
	}

	if !duplicate.Duplicate || duplicate.Document.ID != first.Document.ID || duplicate.Job.ID != first.Job.ID {
		t.Fatalf("duplicate did not reuse existing document and job: %+v", duplicate)
	}
	if otherTenant.Duplicate || otherTenant.Document.ID == first.Document.ID {
		t.Fatalf("content deduplication must remain tenant scoped: %+v", otherTenant)
	}
	if len(repository.documents) != 2 {
		t.Fatalf("expected one document per tenant, got %d", len(repository.documents))
	}
	entries, err := os.ReadDir(storageRoot)
	if err != nil || len(entries) != 2 {
		t.Fatalf("duplicate temporary storage should be removed, entries=%d, err=%v", len(entries), err)
	}
}

func TestAcceptRejectsUnsupportedOversizedAndBinaryContent(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		content  []byte
		maxBytes int64
		code     string
	}{
		{name: "extension", filename: "project.pdf", content: []byte("plain text"), maxBytes: 1024, code: "DOCUMENT_TYPE_UNSUPPORTED"},
		{name: "too large", filename: "project.txt", content: []byte("12345"), maxBytes: 4, code: "DOCUMENT_TOO_LARGE"},
		{name: "binary", filename: "project.txt", content: []byte{0, 1, 2, 3, 4}, maxBytes: 1024, code: "DOCUMENT_CONTENT_INVALID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := new(memoryRepository)
			storageRoot := t.TempDir()
			service, err := NewService(repository, storageRoot, test.maxBytes, fixedClock{value: time.Now().UTC()}, new(sequenceIDs))
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Accept(context.Background(), AcceptInput{TenantID: "tenant", UserID: "user", TraceID: "trace", File: multipartFile(t, test.filename, test.content)})
			var domainError *contract.DomainError
			if !errors.As(err, &domainError) || domainError.Code != test.code {
				t.Fatalf("expected %s, got %v", test.code, err)
			}
			if len(repository.documents) != 0 {
				t.Fatal("rejected content must not create database records")
			}
			entries, readErr := os.ReadDir(storageRoot)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("rejected content must not leave storage, entries=%d, err=%v", len(entries), readErr)
			}
		})
	}
}

func TestJobLookupDoesNotCrossTenantBoundary(t *testing.T) {
	repository := &memoryRepository{jobs: []model.KnowledgeJob{{ID: "job-a", TenantID: "tenant-a"}}}
	service, err := NewService(repository, filepath.Join(t.TempDir(), "knowledge"), 1024, fixedClock{value: time.Now().UTC()}, new(sequenceIDs))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Job(context.Background(), "tenant-b", "job-a")
	var domainError *contract.DomainError
	if !errors.As(err, &domainError) || domainError.Code != "KNOWLEDGE_JOB_NOT_FOUND" {
		t.Fatalf("cross-tenant job lookup should look like not found, got %v", err)
	}
}

func multipartFile(t *testing.T, filename string, content []byte) *multipart.FileHeader {
	t.Helper()
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if err := request.ParseMultipartForm(int64(len(content) + 1024)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = request.MultipartForm.RemoveAll() })
	return request.MultipartForm.File["file"][0]
}
