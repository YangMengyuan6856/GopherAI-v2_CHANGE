package evaluation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"GopherAI/internal/catalogreview"
	"GopherAI/internal/catalogseal"

	"github.com/gin-gonic/gin"
)

type catalogSealServiceStub struct {
	status    catalogseal.Status
	receipt   catalogseal.Receipt
	err       error
	reviewer  string
	command   catalogseal.Command
	statusHit bool
}

func (stub *catalogSealServiceStub) Status(_ context.Context, reviewer string) (catalogseal.Status, error) {
	stub.reviewer, stub.statusHit = reviewer, true
	return stub.status, stub.err
}

func (stub *catalogSealServiceStub) Seal(_ context.Context, reviewer string, command catalogseal.Command) (catalogseal.Receipt, error) {
	stub.reviewer, stub.command = reviewer, command
	return stub.receipt, stub.err
}

func TestCatalogSealHandlerStatusAndStrictSealRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &catalogSealServiceStub{status: catalogseal.Status{
		SchemaVersion: catalogseal.StatusSchemaVersion, Status: "blocked_human_review", CatalogSHA256: strings.Repeat("a", 64), ReviewSetSHA: strings.Repeat("b", 64),
		Progress: catalogreview.Progress{Total: 320, Pending: 320, ReviewSetSHA256: strings.Repeat("b", 64)},
	}}
	handler := NewCatalogSealHandler(stub)
	statusRecorder := httptest.NewRecorder()
	statusContext, _ := gin.CreateTestContext(statusRecorder)
	statusContext.Request = httptest.NewRequest(http.MethodGet, "/evaluations/catalog/reviews/seal/latest", nil)
	statusContext.Set("userName", "alice")
	handler.Latest(statusContext)
	if statusRecorder.Code != http.StatusOK || !stub.statusHit || stub.reviewer != "alice" || !strings.Contains(statusRecorder.Body.String(), `"pending":320`) {
		t.Fatalf("unexpected status response: %d %s", statusRecorder.Code, statusRecorder.Body.String())
	}

	body := `{"mode":"materialize_reviewed_catalog_candidate","catalog_sha256":"` + strings.Repeat("a", 64) + `","review_set_sha256":"` + strings.Repeat("b", 64) + `","acknowledgment":"` + catalogseal.Acknowledgment + `"}`
	stub.receipt = catalogseal.Receipt{Created: true, Report: catalogseal.Report{SchemaVersion: catalogseal.SchemaVersion}}
	sealRecorder := httptest.NewRecorder()
	sealContext, _ := gin.CreateTestContext(sealRecorder)
	sealContext.Request = httptest.NewRequest(http.MethodPost, "/evaluations/catalog/reviews/seal", strings.NewReader(body))
	sealContext.Set("userName", "alice")
	handler.Seal(sealContext)
	if sealRecorder.Code != http.StatusOK || stub.command.Acknowledgment != catalogseal.Acknowledgment || stub.command.ReviewSetSHA256 != strings.Repeat("b", 64) {
		t.Fatalf("unexpected seal response: %d %s command=%+v", sealRecorder.Code, sealRecorder.Body.String(), stub.command)
	}

	badRecorder := httptest.NewRecorder()
	badContext, _ := gin.CreateTestContext(badRecorder)
	badContext.Request = httptest.NewRequest(http.MethodPost, "/evaluations/catalog/reviews/seal", strings.NewReader(body[:len(body)-1]+`,"unexpected":true}`))
	handler.Seal(badContext)
	if badRecorder.Code != http.StatusBadRequest || !strings.Contains(badRecorder.Body.String(), "INVALID_CATALOG_SEAL_REQUEST") {
		t.Fatalf("unexpected strict decode response: %d %s", badRecorder.Code, badRecorder.Body.String())
	}
}

func TestCatalogSealHandlerMapsIncompleteAndStaleWithoutWritingSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, item := range []struct {
		err  error
		code string
	}{
		{err: catalogseal.ErrInvalidCommand, code: "INVALID_CATALOG_SEAL_REQUEST"},
		{err: catalogseal.ErrReviewIncomplete, code: "CATALOG_REVIEW_INCOMPLETE"},
		{err: catalogseal.ErrStaleReview, code: "CATALOG_REVIEW_STALE"},
		{err: catalogseal.ErrInvalidArtifact, code: "CATALOG_SEAL_INVALID"},
		{err: errors.New("storage"), code: "CATALOG_SEAL_UNAVAILABLE"},
	} {
		stub := &catalogSealServiceStub{err: item.err}
		handler := NewCatalogSealHandler(stub)
		body := `{"mode":"materialize_reviewed_catalog_candidate","catalog_sha256":"` + strings.Repeat("a", 64) + `","review_set_sha256":"` + strings.Repeat("b", 64) + `","acknowledgment":"` + catalogseal.Acknowledgment + `"}`
		recorder := httptest.NewRecorder()
		ginContext, _ := gin.CreateTestContext(recorder)
		ginContext.Request = httptest.NewRequest(http.MethodPost, "/evaluations/catalog/reviews/seal", strings.NewReader(body))
		handler.Seal(ginContext)
		if recorder.Code < 400 || !strings.Contains(recorder.Body.String(), item.code) {
			t.Fatalf("error %v mapped incorrectly: %d %s", item.err, recorder.Code, recorder.Body.String())
		}
	}
}
