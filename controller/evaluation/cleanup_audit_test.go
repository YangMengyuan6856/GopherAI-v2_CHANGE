package evaluation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"GopherAI/internal/cleanupaudit"

	"github.com/gin-gonic/gin"
)

type memoryCleanupAuditStore struct {
	report cleanupaudit.Report
	err    error
}

func (store *memoryCleanupAuditStore) Load() (cleanupaudit.Report, error) {
	return store.report, store.err
}
func (store *memoryCleanupAuditStore) Save(report cleanupaudit.Report) error {
	store.report = report
	store.err = nil
	return nil
}

func TestCleanupAuditAcceptancePersistsReport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &memoryCleanupAuditStore{err: errors.New("not ready")}
	expected := cleanupaudit.Report{SchemaVersion: cleanupaudit.SchemaVersion, ReleaseID: "release-1"}
	handler := NewCleanupAuditHandler(func(context.Context) (cleanupaudit.Report, error) { return expected, nil }, store)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	handler.Acceptance(ctx)
	if recorder.Code != http.StatusOK || store.report.ReleaseID != "release-1" {
		t.Fatalf("unexpected response %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCleanupAuditLatestFailsClosedWhenMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewCleanupAuditHandler(nil, &memoryCleanupAuditStore{err: errors.New("missing")})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	handler.Latest(ctx)
	if recorder.Code != http.StatusNotFound || !containsAll(recorder.Body.String(), "CLEANUP_AUDIT_NOT_READY", "请先运行") {
		t.Fatalf("unexpected response %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
