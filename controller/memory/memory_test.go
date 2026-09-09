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
	profiledomain "GopherAI/internal/profilememory"

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

type fakeProfileService struct {
	list         profiledomain.ListResponse
	corrected    profiledomain.PublicMemory
	err          error
	userID       string
	memoryID     string
	value        string
	deleted      bool
	recall       profiledomain.RecallResponse
	recallTenant string
	recallUser   string
	recallQuery  string
}

func (service *fakeProfileService) Recall(_ context.Context, tenantID, userID, query string, _ int) (profiledomain.RecallResponse, error) {
	service.recallTenant, service.recallUser, service.recallQuery = tenantID, userID, query
	if service.recall.PolicyVersion == "" {
		return profiledomain.RecallResponse{SchemaVersion: profiledomain.SchemaVersion, PolicyVersion: profiledomain.RecallPolicyVersion, Status: "no_match", Items: []profiledomain.PublicMemory{}}, nil
	}
	return service.recall, nil
}

func (service *fakeProfileService) List(_ context.Context, userID string) (profiledomain.ListResponse, error) {
	service.userID = userID
	return service.list, service.err
}

func (service *fakeProfileService) Correct(_ context.Context, command profiledomain.Correction) (profiledomain.PublicMemory, error) {
	service.userID, service.memoryID, service.value = command.UserID, command.MemoryID, command.Value
	return service.corrected, service.err
}

func (service *fakeProfileService) Delete(_ context.Context, userID string, memoryID string) error {
	service.userID, service.memoryID, service.deleted = userID, memoryID, true
	return service.err
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

func TestPreviewShowsOnlyGovernedRelevantProfileInActualAssembly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeMemoryService{preview: memorydomain.Preview{SchemaVersion: memorydomain.SchemaVersion, Window: memorydomain.WorkingWindow{
		SessionID: "session-1", Cache: memorydomain.CacheHit,
		Messages: []memorydomain.WorkingMessage{{ID: 1, Role: memorydomain.RoleUser, Content: "我当前 Redis 版本是什么？"}},
	}}}
	profiles := &fakeProfileService{recall: profiledomain.RecallResponse{
		SchemaVersion: profiledomain.SchemaVersion, PolicyVersion: profiledomain.RecallPolicyVersion, Status: "hit",
		Items: []profiledomain.PublicMemory{{Key: "redis_version", Value: "7.4", Confidence: 1, Status: profiledomain.StatusActive}},
	}}
	context, recorder := memoryContext(http.MethodGet, "/api/v1/memory/sessions/session-1/context?budget_tokens=512", "session-1")
	NewHandlerWithProfiles(service, profiles).Preview(context)
	if recorder.Code != http.StatusOK || profiles.recallTenant != "alice" || profiles.recallUser != "alice" || profiles.recallQuery == "" {
		t.Fatalf("profile recall principal/query missing: code=%d profiles=%+v body=%s", recorder.Code, profiles, recorder.Body.String())
	}
	var response memorydomain.Preview
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ProfileRecall == nil || response.ProfileRecall.Status != "hit" || response.Context.ProfileIncluded != 1 {
		t.Fatalf("governed profile not visible in assembly: %#v", response)
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

func TestProfileMemoryCRUDUsesAuthenticatedPrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	profiles := &fakeProfileService{list: profiledomain.ListResponse{SchemaVersion: profiledomain.SchemaVersion}, corrected: profiledomain.PublicMemory{ID: "memory-1-v2", Status: profiledomain.StatusActive}}
	handler := NewHandlerWithProfiles(new(fakeMemoryService), profiles)

	listContext, listRecorder := memoryContext(http.MethodGet, "/api/v1/memory/profiles", "")
	handler.ListProfiles(listContext)
	if listRecorder.Code != http.StatusOK || profiles.userID != "alice" {
		t.Fatalf("profile list principal missing: code=%d service=%+v", listRecorder.Code, profiles)
	}

	correctContext, correctRecorder := memoryContext(http.MethodPatch, "/api/v1/memory/profiles/memory-1", "")
	correctContext.Params = gin.Params{{Key: "memory_id", Value: "memory-1"}}
	correctContext.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/memory/profiles/memory-1", bytes.NewBufferString(`{"value":"Redis 7.4"}`))
	correctContext.Request.Header.Set("Content-Type", "application/json")
	handler.CorrectProfile(correctContext)
	if correctRecorder.Code != http.StatusOK || profiles.userID != "alice" || profiles.memoryID != "memory-1" || profiles.value != "Redis 7.4" {
		t.Fatalf("profile correction contract failed: code=%d service=%+v body=%s", correctRecorder.Code, profiles, correctRecorder.Body.String())
	}

	deleteContext, deleteRecorder := memoryContext(http.MethodDelete, "/api/v1/memory/profiles/memory-1-v2", "")
	deleteContext.Params = gin.Params{{Key: "memory_id", Value: "memory-1-v2"}}
	handler.DeleteProfile(deleteContext)
	if deleteRecorder.Code != http.StatusNoContent || !profiles.deleted || profiles.userID != "alice" {
		t.Fatalf("profile delete contract failed: code=%d service=%+v", deleteRecorder.Code, profiles)
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
