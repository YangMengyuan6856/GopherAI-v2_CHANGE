package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	memorydomain "GopherAI/internal/memory"

	"github.com/gin-gonic/gin"
)

type fakeMemoryService struct {
	preview memorydomain.Preview
	window  memorydomain.WorkingWindow
	err     error
	userID  string
	session string
	budget  int
}

func (service *fakeMemoryService) Preview(_ context.Context, userID string, sessionID string, budget int) (memorydomain.Preview, error) {
	service.userID, service.session, service.budget = userID, sessionID, budget
	return service.preview, service.err
}

func (service *fakeMemoryService) Rebuild(_ context.Context, userID string, sessionID string) (memorydomain.WorkingWindow, error) {
	service.userID, service.session = userID, sessionID
	return service.window, service.err
}

func TestPreviewReturnsOwnedBoundedContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeMemoryService{preview: memorydomain.Preview{SchemaVersion: memorydomain.SchemaVersion, Window: memorydomain.WorkingWindow{SessionID: "session-1", Cache: memorydomain.CacheHit}}}
	context, recorder := memoryContext(http.MethodGet, "/api/v1/memory/sessions/session-1/context?budget_tokens=512", "session-1")
	NewHandler(service).Preview(context)
	if recorder.Code != http.StatusOK || service.userID != "alice" || service.session != "session-1" || service.budget != 512 {
		t.Fatalf("unexpected preview: code=%d service=%+v body=%s", recorder.Code, service, recorder.Body.String())
	}
}

func TestPreviewRejectsUnboundedBudgetBeforeServiceCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := new(fakeMemoryService)
	context, recorder := memoryContext(http.MethodGet, "/api/v1/memory/sessions/session-1/context?budget_tokens=99999", "session-1")
	NewHandler(service).Preview(context)
	if recorder.Code != http.StatusBadRequest || service.session != "" {
		t.Fatalf("invalid budget reached service: code=%d service=%+v", recorder.Code, service)
	}
}

func TestCrossUserSessionIsHiddenAs404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeMemoryService{err: memorydomain.ErrSessionNotFound}
	context, recorder := memoryContext(http.MethodGet, "/api/v1/memory/sessions/secret/context", "secret")
	NewHandler(service).Preview(context)
	if recorder.Code != http.StatusNotFound || bytes.Contains(recorder.Body.Bytes(), []byte("authority")) {
		t.Fatalf("unexpected hidden-session response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestRebuildReturnsExplicitCacheProvenance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeMemoryService{window: memorydomain.WorkingWindow{SessionID: "session-1", Cache: memorydomain.CacheRebuilt}}
	context, recorder := memoryContext(http.MethodPost, "/api/v1/memory/sessions/session-1/rebuild", "session-1")
	NewHandler(service).Rebuild(context)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected rebuild code: %d %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	window := response["window"].(map[string]any)
	if window["cache_status"] != string(memorydomain.CacheRebuilt) {
		t.Fatalf("cache provenance missing: %s", recorder.Body.String())
	}
}

func TestDependencyFailureDoesNotLeakInternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeMemoryService{err: errors.New("redis password=secret")}
	context, recorder := memoryContext(http.MethodGet, "/api/v1/memory/sessions/session-1/context", "session-1")
	NewHandler(service).Preview(context)
	if recorder.Code != http.StatusServiceUnavailable || bytes.Contains(recorder.Body.Bytes(), []byte("secret")) {
		t.Fatalf("internal error leaked: %d %s", recorder.Code, recorder.Body.String())
	}
}

func memoryContext(method string, target string, sessionID string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("userName", "alice")
	context.Params = gin.Params{{Key: "session_id", Value: sessionID}}
	context.Request = httptest.NewRequest(method, target, nil)
	return context, recorder
}
