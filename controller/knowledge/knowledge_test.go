package knowledge

import (
	"GopherAI/internal/contract"
	knowledgeapp "GopherAI/internal/knowledge"
	"GopherAI/middleware/requestid"
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type fakeApplication struct {
	acceptInput knowledgeapp.AcceptInput
	accept      knowledgeapp.AcceptResult
	err         error
}

func (application *fakeApplication) Accept(_ context.Context, input knowledgeapp.AcceptInput) (knowledgeapp.AcceptResult, error) {
	application.acceptInput = input
	return application.accept, application.err
}

func (application *fakeApplication) List(context.Context, string) ([]knowledgeapp.DocumentSummary, error) {
	return nil, application.err
}

func (application *fakeApplication) Job(context.Context, string, string) (knowledgeapp.JobSummary, error) {
	return knowledgeapp.JobSummary{}, application.err
}

type fakeMetrics struct {
	status string
	size   int64
}

func (metrics *fakeMetrics) RecordDocumentUpload(status string, sizeBytes int64) {
	metrics.status = status
	metrics.size = sizeBytes
}

func TestUploadReturnsAcceptedContractAndTrace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	application := &fakeApplication{accept: knowledgeapp.AcceptResult{
		Document: knowledgeapp.DocumentSummary{ID: "document-1", DisplayName: "project.md", SizeBytes: 42, Status: knowledgeapp.DocumentStatusUploaded},
		Job:      knowledgeapp.JobSummary{ID: "job-1", DocumentID: "document-1", Status: knowledgeapp.JobStatusQueued},
	}}
	metrics := new(fakeMetrics)
	handler := NewHandler(application, metrics)
	engine := gin.New()
	engine.POST("/documents", requestid.Attach(), func(context *gin.Context) {
		context.Set("userName", "user-a")
		context.Next()
	}, handler.Upload)

	request := uploadRequest(t, "project.md", []byte("project content"))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", response.Code, response.Body.String())
	}
	var payload uploadResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != contract.SchemaVersion || payload.TraceID == "" || payload.Document.ID != "document-1" || payload.Job.ID != "job-1" {
		t.Fatalf("unexpected response: %+v", payload)
	}
	if application.acceptInput.TenantID != "user-a" || application.acceptInput.UserID != "user-a" || application.acceptInput.TraceID != payload.TraceID {
		t.Fatalf("request identity was not propagated: %+v", application.acceptInput)
	}
	if response.Header().Get(requestid.TraceIDHeader) != payload.TraceID {
		t.Fatal("response trace header and body must match")
	}
	if metrics.status != "accepted" || metrics.size != 42 {
		t.Fatalf("unexpected metric: %+v", metrics)
	}
}

func TestUploadMapsValidationErrorWithoutLeakingCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	application := &fakeApplication{err: contract.NewDomainError("DOCUMENT_TYPE_UNSUPPORTED", contract.ErrorValidation, "仅支持 .md 和 .txt 文档", false, context.Canceled)}
	metrics := new(fakeMetrics)
	handler := NewHandler(application, metrics)
	engine := gin.New()
	engine.POST("/documents", requestid.Attach(), func(context *gin.Context) {
		context.Set("userName", "user-a")
		context.Next()
	}, handler.Upload)

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, uploadRequest(t, "project.pdf", []byte("content")))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte("context canceled")) {
		t.Fatalf("internal cause leaked: %s", response.Body.String())
	}
	if metrics.status != "rejected" {
		t.Fatalf("expected rejected metric, got %q", metrics.status)
	}
}

func uploadRequest(t *testing.T, filename string, content []byte) *http.Request {
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
	request := httptest.NewRequest(http.MethodPost, "/documents", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
