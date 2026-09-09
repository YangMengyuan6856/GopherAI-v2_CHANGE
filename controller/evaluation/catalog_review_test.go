package evaluation

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"GopherAI/internal/catalogreview"

	"github.com/gin-gonic/gin"
)

type catalogReviewServiceStub struct {
	workbench catalogreview.Workbench
	receipt   catalogreview.Receipt
	evidence  catalogreview.EvidenceSnapshot
	err       error
	reviewer  string
	query     catalogreview.Query
	command   catalogreview.ReviewCommand
}

func (stub *catalogReviewServiceStub) List(_ context.Context, reviewer string, query catalogreview.Query) (catalogreview.Workbench, error) {
	stub.reviewer, stub.query = reviewer, query
	return stub.workbench, stub.err
}

func (stub *catalogReviewServiceStub) Submit(_ context.Context, reviewer string, command catalogreview.ReviewCommand) (catalogreview.Receipt, error) {
	stub.reviewer, stub.command = reviewer, command
	return stub.receipt, stub.err
}

func (stub *catalogReviewServiceStub) Evidence(_ context.Context, reviewer string) (catalogreview.EvidenceSnapshot, error) {
	stub.reviewer = reviewer
	return stub.evidence, stub.err
}

func TestCatalogReviewHandlerUsesPrincipalAndStrictPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &catalogReviewServiceStub{workbench: catalogreview.Workbench{SchemaVersion: catalogreview.SchemaVersion}}
	router := catalogReviewTestRouter(stub)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/reviews?slice=rag&status=pending&page=2&page_size=5", nil))
	if response.Code != http.StatusOK || stub.reviewer != "alice" || stub.query.Slice != "rag" || stub.query.Status != "pending" || stub.query.Page != 2 || stub.query.PageSize != 5 {
		t.Fatalf("query was not forwarded safely: code=%d reviewer=%s query=%+v body=%s", response.Code, stub.reviewer, stub.query, response.Body.String())
	}
	invalid := httptest.NewRecorder()
	router.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/reviews?page=zero", nil))
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "INVALID_CATALOG_REVIEW_QUERY") {
		t.Fatalf("invalid pagination was accepted: %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestCatalogReviewHandlerRejectsUnknownFieldsAndForwardsCAS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &catalogReviewServiceStub{receipt: catalogreview.Receipt{SchemaVersion: catalogreview.ReviewSchemaVersion}}
	router := catalogReviewTestRouter(stub)
	body := fmt.Sprintf(`{"mode":"%s","catalog_sha256":"%s","governance_sha256":"%s","case_id":"case-1","case_sha256":"%s","expected_revision":2,"decision":"rejected","reason_codes":["missing_context"],"idempotency_key":"catalog-review-handler-0001","acknowledgment":"%s"}`, catalogReviewMode, strings.Repeat("a", 64), strings.Repeat("c", 64), strings.Repeat("b", 64), catalogreview.Acknowledgment)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/reviews", strings.NewReader(body)))
	if response.Code != http.StatusOK || stub.reviewer != "alice" || stub.command.GovernanceSHA256 != strings.Repeat("c", 64) || stub.command.ExpectedRevision != 2 || stub.command.Decision != "rejected" || stub.command.ReasonCodes[0] != "missing_context" {
		t.Fatalf("command was not forwarded safely: code=%d reviewer=%s command=%+v body=%s", response.Code, stub.reviewer, stub.command, response.Body.String())
	}
	unknown := httptest.NewRecorder()
	router.ServeHTTP(unknown, httptest.NewRequest(http.MethodPost, "/reviews", strings.NewReader(strings.TrimSuffix(body, "}")+`,"auto_approve":true}`)))
	if unknown.Code != http.StatusBadRequest || !strings.Contains(unknown.Body.String(), "INVALID_CATALOG_REVIEW") {
		t.Fatalf("unknown field was accepted: %d %s", unknown.Code, unknown.Body.String())
	}
}

func TestCatalogReviewHandlerMapsStaleRevisionToConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &catalogReviewServiceStub{err: catalogreview.ErrRevisionConflict}
	router := catalogReviewTestRouter(stub)
	body := fmt.Sprintf(`{"mode":"%s","catalog_sha256":"%s","governance_sha256":"%s","case_id":"case-1","case_sha256":"%s","expected_revision":0,"decision":"approved","reason_codes":["label_verified"],"idempotency_key":"catalog-review-handler-0002","acknowledgment":"%s"}`, catalogReviewMode, strings.Repeat("a", 64), strings.Repeat("c", 64), strings.Repeat("b", 64), catalogreview.Acknowledgment)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/reviews", strings.NewReader(body)))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "CATALOG_REVIEW_STALE") {
		t.Fatalf("stale review was not mapped to conflict: %d %s", response.Code, response.Body.String())
	}
}

func TestCatalogReviewHandlerDownloadsValidatedEvidenceFormats(t *testing.T) {
	gin.SetMode(gin.TestMode)
	report := catalogreview.EvidenceSnapshot{
		SchemaVersion: catalogreview.EvidenceSchemaVersion, DatasetVersion: "dataset-v1", CatalogSHA256: strings.Repeat("a", 64), GovernanceSHA256: strings.Repeat("b", 64),
		ReviewerScope: "current_authenticated_reviewer", Status: "human_review_in_progress", TotalCases: 3,
		PendingCases: 3, ReviewSetSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Entries: []catalogreview.EvidenceEntry{}, Guardrails: []string{"self_hash_verified"}, Limitations: []string{"not a baseline"},
	}
	if err := catalogreview.FinalizeEvidenceSnapshot(&report); err != nil {
		t.Fatal(err)
	}
	stub := &catalogReviewServiceStub{evidence: report}
	router := catalogReviewTestRouter(stub)
	jsonResponse := httptest.NewRecorder()
	router.ServeHTTP(jsonResponse, httptest.NewRequest(http.MethodGet, "/reviews/evidence?format=json", nil))
	if jsonResponse.Code != http.StatusOK || stub.reviewer != "alice" || !strings.Contains(jsonResponse.Header().Get("Content-Disposition"), ".json") || !strings.Contains(jsonResponse.Body.String(), report.SnapshotSHA256) {
		t.Fatalf("JSON evidence download failed: %d headers=%v body=%s", jsonResponse.Code, jsonResponse.Header(), jsonResponse.Body.String())
	}
	markdownResponse := httptest.NewRecorder()
	router.ServeHTTP(markdownResponse, httptest.NewRequest(http.MethodGet, "/reviews/evidence?format=markdown", nil))
	if markdownResponse.Code != http.StatusOK || !strings.Contains(markdownResponse.Header().Get("Content-Disposition"), ".md") || !strings.Contains(markdownResponse.Body.String(), "0/3") {
		t.Fatalf("Markdown evidence download failed: %d headers=%v body=%s", markdownResponse.Code, markdownResponse.Header(), markdownResponse.Body.String())
	}
	invalid := httptest.NewRecorder()
	router.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/reviews/evidence?format=pdf", nil))
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "INVALID_CATALOG_REVIEW_EVIDENCE_FORMAT") {
		t.Fatalf("invalid evidence format was accepted: %d %s", invalid.Code, invalid.Body.String())
	}
}

func catalogReviewTestRouter(service CatalogReviewService) *gin.Engine {
	handler := NewCatalogReviewHandler(service)
	router := gin.New()
	router.Use(func(context *gin.Context) { context.Set("userName", "alice"); context.Next() })
	router.GET("/reviews", handler.List)
	router.POST("/reviews", handler.Submit)
	router.GET("/reviews/evidence", handler.Evidence)
	return router
}
