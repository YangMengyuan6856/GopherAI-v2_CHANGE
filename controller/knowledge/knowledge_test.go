package knowledge

import (
	knowledgeagent "GopherAI/internal/agent/knowledge"
	"GopherAI/internal/contract"
	knowledgeapp "GopherAI/internal/knowledge"
	ragapp "GopherAI/internal/rag"
	"GopherAI/middleware/requestid"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type fakeApplication struct {
	acceptInput       knowledgeapp.AcceptInput
	accept            knowledgeapp.AcceptResult
	versionDocumentID string
	err               error
}

func (application *fakeApplication) Accept(_ context.Context, input knowledgeapp.AcceptInput) (knowledgeapp.AcceptResult, error) {
	application.acceptInput = input
	return application.accept, application.err
}

func (application *fakeApplication) AcceptVersion(_ context.Context, documentID string, input knowledgeapp.AcceptInput) (knowledgeapp.AcceptResult, error) {
	application.versionDocumentID = documentID
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
	status          string
	size            int64
	retrievalStatus string
	retrievalMode   string
	resultCount     int
	answerStatus    string
	gateReason      string
}

func (metrics *fakeMetrics) RecordKnowledgeAnswer(status string, gateReason string, _ time.Duration) {
	metrics.answerStatus = status
	metrics.gateReason = gateReason
}

func (metrics *fakeMetrics) RecordDocumentUpload(status string, sizeBytes int64) {
	metrics.status = status
	metrics.size = sizeBytes
}

func (metrics *fakeMetrics) RecordKnowledgeRetrieval(status string, mode string, _ time.Duration, resultCount int) {
	metrics.retrievalStatus = status
	metrics.retrievalMode = mode
	metrics.resultCount = resultCount
}

type fakeSearchApplication struct {
	input  ragapp.SearchInput
	output ragapp.SearchOutput
	err    error
}

type fakeAnswerApplication struct {
	input  knowledgeagent.Input
	output knowledgeagent.Output
	err    error
}

func (application *fakeAnswerApplication) Answer(_ context.Context, input knowledgeagent.Input) (knowledgeagent.Output, error) {
	application.input = input
	return application.output, application.err
}

func (application *fakeSearchApplication) Search(_ context.Context, input ragapp.SearchInput) (ragapp.SearchOutput, error) {
	application.input = input
	return application.output, application.err
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

func TestUploadVersionReturnsPendingAliasContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	application := &fakeApplication{accept: knowledgeapp.AcceptResult{
		Document:        knowledgeapp.DocumentSummary{ID: "document-1", CurrentVersion: 1, Status: knowledgeapp.DocumentStatusIndexed},
		Job:             knowledgeapp.JobSummary{ID: "job-2", DocumentID: "document-1", Version: 2, Status: knowledgeapp.JobStatusQueued},
		PreviousVersion: 1,
		PendingVersion:  2,
	}}
	handler := NewHandler(application, new(fakeMetrics))
	engine := gin.New()
	engine.POST("/documents/:document_id/versions", requestid.Attach(), func(context *gin.Context) {
		context.Set("userName", "user-a")
		context.Next()
	}, handler.UploadVersion)

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, uploadRequestFor(t, http.MethodPost, "/documents/document-1/versions", "project.md", []byte("version two")))
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", response.Code, response.Body.String())
	}
	payload := new(uploadResponse)
	if err := json.Unmarshal(response.Body.Bytes(), payload); err != nil {
		t.Fatal(err)
	}
	if payload.PreviousVersion != 1 || payload.PendingVersion != 2 || payload.Document.CurrentVersion != 1 || payload.Job.Version != 2 {
		t.Fatalf("candidate must be visible without moving active alias: %+v", payload)
	}
	if application.versionDocumentID != "document-1" || application.acceptInput.TenantID != "user-a" {
		t.Fatalf("version target or ACL was lost: id=%s input=%+v", application.versionDocumentID, application.acceptInput)
	}
}

func TestUploadMapsValidationErrorWithoutLeakingCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	application := &fakeApplication{err: contract.NewDomainError("DOCUMENT_TYPE_UNSUPPORTED", contract.ErrorValidation, "仅支持 .md、.txt、.json、.yaml、.yml 和 .go 文档", false, context.Canceled)}
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

func TestSearchReturnsEvidenceAndPropagatesAuthenticatedACL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	search := &fakeSearchApplication{output: ragapp.SearchOutput{
		Hits:        []ragapp.SearchHit{{Evidence: contract.Evidence{ID: "chunk-1", Title: "project.md", Content: "evidence"}, DenseRank: 1, KeywordRank: 2, RRFScore: 0.03}},
		Diagnostics: ragapp.SearchDiagnostics{Version: ragapp.RetrievalVersion, Mode: "hybrid", DenseCandidates: 2, KeywordCandidates: 1, FusedCandidates: 1},
	}}
	metrics := new(fakeMetrics)
	handler := NewHandler(new(fakeApplication), metrics)
	handler.search = search
	handler.retrievalMetrics = metrics
	engine := gin.New()
	engine.POST("/search", requestid.Attach(), func(context *gin.Context) {
		context.Set("userName", "user-a")
		context.Next()
	}, handler.Search)

	request := httptest.NewRequest(http.MethodPost, "/search", bytes.NewBufferString(`{"query":"REDIS_TIMEOUT","top_k":3}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if search.input.TenantID != "user-a" || search.input.UserID != "user-a" || search.input.TopK != 3 {
		t.Fatalf("authenticated ACL was not propagated: %+v", search.input)
	}
	var payload searchResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.TraceID == "" || len(payload.Hits) != 1 || payload.Diagnostics.Mode != "hybrid" {
		t.Fatalf("unexpected search response: %+v", payload)
	}
	if metrics.retrievalStatus != "success" || metrics.retrievalMode != "hybrid" || metrics.resultCount != 1 {
		t.Fatalf("unexpected retrieval metric: %+v", metrics)
	}
}

func TestSearchUnavailableDoesNotLeakInitializationCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(new(fakeApplication), new(fakeMetrics))
	handler.searchInitError = errors.New("secret embedding endpoint")
	engine := gin.New()
	engine.POST("/search", requestid.Attach(), func(context *gin.Context) {
		context.Set("userName", "user-a")
		context.Next()
	}, handler.Search)

	request := httptest.NewRequest(http.MethodPost, "/search", bytes.NewBufferString(`{"query":"question"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte("secret embedding endpoint")) {
		t.Fatalf("initialization cause leaked: %s", response.Body.String())
	}
}

func TestAnswerReturnsEvidenceGateAndVerifiedCitationsWithAuthenticatedACL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	answer := &fakeAnswerApplication{output: knowledgeagent.Output{
		Result: contract.AgentResult{
			Answer: "重试次数为 7。[1]", Resolved: true, Confidence: 0.97,
			Citations: []contract.Citation{{ID: "C1", EvidenceID: "chunk-1", Document: "project.md", Version: "1", LineStart: 4, LineEnd: 5}},
			Evidence:  []contract.Evidence{{ID: "chunk-1", TenantID: "user-a", Title: "project.md", Content: "重试次数为 7。"}},
		},
		Gate:        ragapp.EvidenceGateResult{Accepted: true, ReasonCode: ragapp.GateReasonSufficient, TopScore: 0.97, HybridEvidenceCount: 1},
		Diagnostics: ragapp.SearchDiagnostics{Mode: "hybrid", DenseCandidates: 1, KeywordCandidates: 1, FusedCandidates: 1},
	}}
	metrics := new(fakeMetrics)
	handler := NewHandler(new(fakeApplication), metrics)
	handler.answer = answer
	handler.answerMetrics = metrics
	engine := gin.New()
	engine.POST("/answer", requestid.Attach(), func(context *gin.Context) {
		context.Set("userName", "user-a")
		context.Next()
	}, handler.Answer)

	request := httptest.NewRequest(http.MethodPost, "/answer", bytes.NewBufferString(`{"question":"默认重试几次？","top_k":3}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if answer.input.TenantID != "user-a" || answer.input.UserID != "user-a" || answer.input.TopK != 3 {
		t.Fatalf("authenticated ACL was not propagated: %+v", answer.input)
	}
	var payload answerResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Agent != knowledgeagent.AgentName || payload.Strategy != knowledgeagent.StrategyName || !payload.Result.Resolved || len(payload.Result.Citations) != 1 {
		t.Fatalf("unexpected answer response: %+v", payload)
	}
	if metrics.answerStatus != "answered" || metrics.gateReason != ragapp.GateReasonSufficient {
		t.Fatalf("unexpected answer metric: %+v", metrics)
	}
}

func TestAnswerReturnsDeterministicInsufficientEvidenceWithoutError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	answer := &fakeAnswerApplication{output: knowledgeagent.Output{
		Result: contract.AgentResult{Answer: "当前知识库没有找到可用证据，因此没有调用模型生成答案。", NeedsUserInput: true},
		Gate:   ragapp.EvidenceGateResult{ReasonCode: ragapp.GateReasonNoEvidence, FollowUpQuestions: []string{"请上传资料。"}},
	}}
	metrics := new(fakeMetrics)
	handler := NewHandler(new(fakeApplication), metrics)
	handler.answer = answer
	handler.answerMetrics = metrics
	engine := gin.New()
	engine.POST("/answer", requestid.Attach(), func(context *gin.Context) {
		context.Set("userName", "user-a")
		context.Next()
	}, handler.Answer)

	request := httptest.NewRequest(http.MethodPost, "/answer", bytes.NewBufferString(`{"question":"不存在的资料"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || metrics.answerStatus != "insufficient" || metrics.gateReason != ragapp.GateReasonNoEvidence {
		t.Fatalf("insufficient evidence should be a controlled 200 response: code=%d metric=%+v body=%s", response.Code, metrics, response.Body.String())
	}
}

func uploadRequest(t *testing.T, filename string, content []byte) *http.Request {
	return uploadRequestFor(t, http.MethodPost, "/documents", filename, content)
}

func uploadRequestFor(t *testing.T, method string, target string, filename string, content []byte) *http.Request {
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
	request := httptest.NewRequest(method, target, body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
