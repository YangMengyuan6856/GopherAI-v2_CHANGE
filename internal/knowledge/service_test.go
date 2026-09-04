package knowledge

import (
	"GopherAI/internal/contract"
	"GopherAI/model"
	"bytes"
	"context"
	"errors"
	"fmt"
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

func (repository *memoryRepository) FindDocument(_ context.Context, tenantID string, userID string, documentID string) (*model.KnowledgeDocument, error) {
	for index := range repository.documents {
		document := &repository.documents[index]
		if document.ID == documentID && document.TenantID == tenantID && document.UserID == userID && document.Status != DocumentStatusDeleted {
			return document, nil
		}
	}
	return nil, nil
}

func (repository *memoryRepository) FindVersionByContentHash(_ context.Context, tenantID string, documentID string, contentHash string) (*model.KnowledgeDocumentVersion, *model.KnowledgeJob, error) {
	for versionIndex := range repository.versions {
		version := &repository.versions[versionIndex]
		if version.DocumentID != documentID || version.ContentHash != contentHash {
			continue
		}
		for jobIndex := range repository.jobs {
			job := &repository.jobs[jobIndex]
			if job.TenantID == tenantID && job.DocumentID == documentID && job.Version == version.Version {
				return version, job, nil
			}
		}
		return version, nil, nil
	}
	return nil, nil, nil
}

func (repository *memoryRepository) FindVersion(_ context.Context, tenantID string, documentID string, versionNumber int) (*model.KnowledgeDocumentVersion, error) {
	foundDocument := false
	for _, document := range repository.documents {
		if document.ID == documentID && document.TenantID == tenantID && document.Status != DocumentStatusDeleted {
			foundDocument = true
			break
		}
	}
	if !foundDocument {
		return nil, nil
	}
	for index := range repository.versions {
		if repository.versions[index].DocumentID == documentID && repository.versions[index].Version == versionNumber {
			return &repository.versions[index], nil
		}
	}
	return nil, nil
}

func (repository *memoryRepository) CreateUpload(_ context.Context, document *model.KnowledgeDocument, version *model.KnowledgeDocumentVersion, job *model.KnowledgeJob, event *model.OutboxEvent) error {
	repository.documents = append(repository.documents, *document)
	repository.versions = append(repository.versions, *version)
	repository.jobs = append(repository.jobs, *job)
	repository.events = append(repository.events, *event)
	return nil
}

func (repository *memoryRepository) CreateVersionUpload(_ context.Context, tenantID string, userID string, version *model.KnowledgeDocumentVersion, job *model.KnowledgeJob, event *model.OutboxEvent) (*model.KnowledgeDocument, error) {
	document, _ := repository.FindDocument(context.Background(), tenantID, userID, version.DocumentID)
	if document == nil {
		return nil, errors.New("document not found")
	}
	latest := 0
	for _, existing := range repository.versions {
		if existing.DocumentID == document.ID && existing.Version > latest {
			latest = existing.Version
		}
	}
	version.Version = latest + 1
	job.TenantID, job.DocumentID, job.Version = tenantID, document.ID, version.Version
	event.TenantID, event.AggregateID, event.AggregateVersion = tenantID, document.ID, version.Version
	event.PayloadJSON = fmt.Sprintf(`{"job_id":%q,"document_id":%q,"version":%d}`, job.ID, document.ID, version.Version)
	repository.versions = append(repository.versions, *version)
	repository.jobs = append(repository.jobs, *job)
	repository.events = append(repository.events, *event)
	return document, nil
}

func (repository *memoryRepository) CreateDelete(_ context.Context, tenantID string, userID string, documentID string, job *model.KnowledgeJob, event *model.OutboxEvent) (*model.KnowledgeDocument, *model.KnowledgeJob, bool, error) {
	document, _ := repository.FindDocument(context.Background(), tenantID, userID, documentID)
	if document == nil {
		for index := range repository.documents {
			candidate := &repository.documents[index]
			if candidate.ID == documentID && candidate.TenantID == tenantID && candidate.UserID == userID && candidate.Status == DocumentStatusDeleted {
				for jobIndex := range repository.jobs {
					existing := &repository.jobs[jobIndex]
					if existing.DocumentID == documentID && existing.JobType == JobTypeDocumentDelete {
						return candidate, existing, true, nil
					}
				}
			}
		}
		return nil, nil, false, errors.New("record not found")
	}
	for _, existing := range repository.jobs {
		if existing.DocumentID == documentID && existing.JobType == JobTypeDocumentIndex &&
			(existing.Status == JobStatusQueued || existing.Status == JobStatusProcessing || existing.Status == JobStatusRetrying) {
			return nil, nil, false, ErrDocumentHasPendingIndex
		}
	}
	document.Status = DocumentStatusDeleted
	job.TenantID, job.DocumentID, job.Version = tenantID, documentID, document.CurrentVersion
	event.TenantID, event.AggregateID, event.AggregateVersion = tenantID, documentID, document.CurrentVersion
	event.PayloadJSON = fmt.Sprintf(`{"job_id":%q,"document_id":%q,"version":%d}`, job.ID, documentID, document.CurrentVersion)
	repository.jobs = append(repository.jobs, *job)
	repository.events = append(repository.events, *event)
	return document, &repository.jobs[len(repository.jobs)-1], false, nil
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

func TestAcceptSupportsStructuredAndGoDocumentFormats(t *testing.T) {
	tests := []struct {
		filename       string
		content        string
		parserVersion  string
		chunkerVersion string
	}{
		{filename: "config.json", content: "{\"retry\":7}\n", parserVersion: ParserVersionDataV1, chunkerVersion: ChunkerVersionDataV1},
		{filename: "config.yaml", content: "retry: 7\n", parserVersion: ParserVersionDataV1, chunkerVersion: ChunkerVersionDataV1},
		{filename: "config.yml", content: "retry: 9\n", parserVersion: ParserVersionDataV1, chunkerVersion: ChunkerVersionDataV1},
		{filename: "worker.go", content: "package worker\n\nfunc Run() {}\n", parserVersion: ParserVersionGoV1, chunkerVersion: ChunkerVersionGoV1},
	}
	for _, test := range tests {
		t.Run(test.filename, func(t *testing.T) {
			repository := new(memoryRepository)
			service, err := NewService(repository, t.TempDir(), DefaultMaxUploadBytes, fixedClock{value: time.Now().UTC()}, new(sequenceIDs))
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.Accept(context.Background(), AcceptInput{
				TenantID: "tenant", UserID: "user", TraceID: "trace", File: multipartFile(t, test.filename, []byte(test.content)),
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Document.DisplayName != test.filename || len(repository.versions) != 1 {
				t.Fatalf("unexpected accepted document: %+v versions=%+v", result, repository.versions)
			}
			version := repository.versions[0]
			if version.ParserVersion != test.parserVersion || version.ChunkerVersion != test.chunkerVersion {
				t.Fatalf("unexpected parser metadata: %+v", version)
			}
		})
	}
}

func TestAcceptDoesNotDeduplicateAcrossParserFamilies(t *testing.T) {
	repository := new(memoryRepository)
	service, err := NewService(repository, t.TempDir(), DefaultMaxUploadBytes, fixedClock{value: time.Now().UTC()}, new(sequenceIDs))
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("retry: 7\n")
	plain, err := service.Accept(context.Background(), AcceptInput{TenantID: "tenant", UserID: "user", TraceID: "plain", File: multipartFile(t, "notes.txt", content)})
	if err != nil {
		t.Fatal(err)
	}
	structured, err := service.Accept(context.Background(), AcceptInput{TenantID: "tenant", UserID: "user", TraceID: "structured", File: multipartFile(t, "config.yaml", content)})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Duplicate || structured.Duplicate || plain.Document.ID == structured.Document.ID || len(repository.documents) != 2 {
		t.Fatalf("different parser families must have distinct identities: plain=%+v structured=%+v", plain, structured)
	}
	if repository.documents[0].ContentHash == repository.documents[1].ContentHash {
		t.Fatal("parser-family document fingerprints must differ")
	}
}

func TestAcceptVersionCreatesPendingVersionWithoutMovingActiveAlias(t *testing.T) {
	repository := new(memoryRepository)
	service, err := NewService(repository, t.TempDir(), DefaultMaxUploadBytes, fixedClock{value: time.Now().UTC()}, new(sequenceIDs))
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Accept(context.Background(), AcceptInput{
		TenantID: "tenant", UserID: "user", TraceID: "trace-v1", File: multipartFile(t, "runbook.md", []byte("# Runbook\nOLD_ALIAS_314\n")),
	})
	if err != nil {
		t.Fatal(err)
	}
	repository.documents[0].Status = DocumentStatusIndexed
	result, err := service.AcceptVersion(context.Background(), first.Document.ID, AcceptInput{
		TenantID: "tenant", UserID: "user", TraceID: "trace-v2", File: multipartFile(t, "runbook.md", []byte("# Runbook\nNEW_ALIAS_926\n")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.PreviousVersion != 1 || result.PendingVersion != 2 || result.Job.Version != 2 {
		t.Fatalf("unexpected version response: %+v", result)
	}
	if repository.documents[0].CurrentVersion != 1 || repository.documents[0].Status != DocumentStatusIndexed {
		t.Fatalf("pending upload must not move active alias: %+v", repository.documents[0])
	}
	if len(repository.versions) != 2 || len(repository.jobs) != 2 || len(repository.events) != 2 {
		t.Fatalf("version upload must persist one version/job/event: %+v", repository)
	}
	if !strings.Contains(repository.events[1].PayloadJSON, `"version":2`) {
		t.Fatalf("event must target allocated version: %s", repository.events[1].PayloadJSON)
	}
	if _, err := os.Stat(repository.versions[1].StoragePath); err != nil {
		t.Fatalf("version artifact was not retained: %v", err)
	}
}

func TestAcceptVersionIsIdempotentAndTenantScoped(t *testing.T) {
	repository := new(memoryRepository)
	service, err := NewService(repository, t.TempDir(), DefaultMaxUploadBytes, fixedClock{value: time.Now().UTC()}, new(sequenceIDs))
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Accept(context.Background(), AcceptInput{
		TenantID: "tenant", UserID: "user", TraceID: "trace-v1", File: multipartFile(t, "runbook.md", []byte("v1")),
	})
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("v2")
	created, err := service.AcceptVersion(context.Background(), first.Document.ID, AcceptInput{
		TenantID: "tenant", UserID: "user", TraceID: "trace-v2", File: multipartFile(t, "runbook.md", content),
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.AcceptVersion(context.Background(), first.Document.ID, AcceptInput{
		TenantID: "tenant", UserID: "user", TraceID: "trace-v2-again", File: multipartFile(t, "renamed.md", content),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate.Duplicate || duplicate.PendingVersion != created.PendingVersion || duplicate.Job.ID != created.Job.ID || len(repository.versions) != 2 {
		t.Fatalf("duplicate version must reuse pending work: created=%+v duplicate=%+v", created, duplicate)
	}
	_, err = service.AcceptVersion(context.Background(), first.Document.ID, AcceptInput{
		TenantID: "other", UserID: "other", TraceID: "cross-tenant", File: multipartFile(t, "runbook.md", []byte("v3")),
	})
	var domainError *contract.DomainError
	if !errors.As(err, &domainError) || domainError.Code != "KNOWLEDGE_DOCUMENT_NOT_FOUND" {
		t.Fatalf("cross-tenant version upload must look absent, got %v", err)
	}
}

func TestRebuildCreatesIndependentCandidateFromActiveArtifact(t *testing.T) {
	repository := new(memoryRepository)
	service, err := NewService(repository, t.TempDir(), DefaultMaxUploadBytes, fixedClock{value: time.Now().UTC()}, new(sequenceIDs))
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Accept(context.Background(), AcceptInput{
		TenantID: "tenant", UserID: "user", TraceID: "initial", File: multipartFile(t, "runbook.md", []byte("rebuild source")),
	})
	if err != nil {
		t.Fatal(err)
	}
	repository.documents[0].Status = DocumentStatusIndexed
	repository.versions[0].Status = DocumentStatusIndexed
	result, err := service.Rebuild(context.Background(), "tenant", "user", "rebuild-trace", first.Document.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.PreviousVersion != 1 || result.PendingVersion != 2 || result.Job.Version != 2 || repository.documents[0].CurrentVersion != 1 {
		t.Fatalf("rebuild must stage v2 without moving active v1: %+v document=%+v", result, repository.documents[0])
	}
	if repository.versions[1].StoragePath != repository.versions[0].StoragePath || repository.versions[1].ContentHash != repository.versions[0].ContentHash {
		t.Fatalf("rebuild candidate must reuse immutable active artifact: %+v", repository.versions)
	}
}

func TestDeleteRevokesAuthorityImmediatelyAndIsIdempotent(t *testing.T) {
	repository := new(memoryRepository)
	service, err := NewService(repository, t.TempDir(), DefaultMaxUploadBytes, fixedClock{value: time.Now().UTC()}, new(sequenceIDs))
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Accept(context.Background(), AcceptInput{
		TenantID: "tenant", UserID: "user", TraceID: "initial", File: multipartFile(t, "obsolete.md", []byte("DELETE-ME-731")),
	})
	if err != nil {
		t.Fatal(err)
	}
	repository.documents[0].Status = DocumentStatusIndexed
	repository.jobs[0].Status = JobStatusCompleted
	deleted, err := service.Delete(context.Background(), "tenant", "user", "delete-trace", first.Document.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Document.Status != DocumentStatusDeleted || deleted.Job.JobType != JobTypeDocumentDelete || deleted.Job.Status != JobStatusQueued {
		t.Fatalf("unexpected delete contract: %+v", deleted)
	}
	documents, err := service.List(context.Background(), "tenant")
	if err != nil || len(documents) != 0 {
		t.Fatalf("deleted document must disappear from authority immediately: %+v err=%v", documents, err)
	}
	again, err := service.Delete(context.Background(), "tenant", "user", "delete-again", first.Document.ID)
	if err != nil || !again.Duplicate || again.Job.ID != deleted.Job.ID || len(repository.events) != 2 {
		t.Fatalf("repeated delete must reuse cleanup job: first=%+v again=%+v err=%v", deleted, again, err)
	}
}

func TestDeleteWaitsForActiveIndexWork(t *testing.T) {
	repository := new(memoryRepository)
	service, err := NewService(repository, t.TempDir(), DefaultMaxUploadBytes, fixedClock{value: time.Now().UTC()}, new(sequenceIDs))
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Accept(context.Background(), AcceptInput{
		TenantID: "tenant", UserID: "user", TraceID: "initial", File: multipartFile(t, "pending.md", []byte("pending index")),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Delete(context.Background(), "tenant", "user", "delete", first.Document.ID)
	var domainError *contract.DomainError
	if !errors.As(err, &domainError) || domainError.Code != "DOCUMENT_INDEX_IN_PROGRESS" || domainError.Category != contract.ErrorConflict {
		t.Fatalf("delete must not race active index work, got %v", err)
	}
	if repository.documents[0].Status == DocumentStatusDeleted {
		t.Fatal("conflicted delete must not revoke document")
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
