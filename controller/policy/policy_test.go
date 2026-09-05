package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"GopherAI/internal/contract"
	policydomain "GopherAI/internal/policy"
	"GopherAI/middleware/requestid"
	"GopherAI/model"

	"github.com/gin-gonic/gin"
)

type fakeService struct {
	snapshot      policydomain.PolicySnapshot
	selection     policydomain.SelectionResult
	err           error
	simulateCalls int
	userID        string
}

func (service *fakeService) Snapshot(context.Context) (policydomain.PolicySnapshot, error) {
	return service.snapshot, service.err
}

func (service *fakeService) Simulate(_ context.Context, userID string, _ string, _ map[policydomain.Dependency]bool, _ contract.ExecutionBudgets) (policydomain.SelectionResult, policydomain.PolicySnapshot, error) {
	service.simulateCalls++
	service.userID = userID
	return service.selection, service.snapshot, service.err
}

type fakeHealth map[policydomain.Dependency]bool

func (health fakeHealth) Snapshot(context.Context) map[policydomain.Dependency]bool { return health }

func policySnapshot() policydomain.PolicySnapshot {
	document := policydomain.DefaultRoutingPolicy()
	return policydomain.PolicySnapshot{
		LoadedPolicy: policydomain.LoadedPolicy{
			Record:   model.RoutingPolicy{Version: document.Version, Environment: policydomain.DefaultPolicyEnvironment, Status: policydomain.PolicyStatusActive, PolicyHash: strings.Repeat("a", 64)},
			Document: document, Source: "redis",
		},
		Registry: policydomain.DefaultStrategyRegistry().List(),
	}
}

func newTestRouter(service Service, health DependencyHealth) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(requestid.Attach(), func(ctx *gin.Context) { ctx.Set("userName", "alice"); ctx.Next() })
	handler := NewHandler(service, health)
	engine.GET("/active", handler.Active)
	engine.POST("/simulate", handler.Simulate)
	return engine
}

func TestActiveExposesAuditableShadowOnlyPolicy(t *testing.T) {
	engine := newTestRouter(&fakeService{snapshot: policySnapshot()}, fakeHealth{})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/active", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	var body SnapshotResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Mode != "shadow_only" || body.AffectsLiveTraffic || body.Policy.Source != "redis" || len(body.Registry) != 7 || len(body.Rules) != 8 {
		t.Fatalf("policy snapshot omitted control boundaries: %+v", body)
	}
}

func TestSimulateUsesAuthenticatedPrincipalAndServerHealth(t *testing.T) {
	service := &fakeService{snapshot: policySnapshot(), selection: policydomain.SelectionResult{Decision: contract.StrategyDecision{StrategyName: "rag_fast", PolicyVersion: "routing-policy-v1"}, Bucket: 42}}
	health := fakeHealth{policydomain.DependencyModel: true, policydomain.DependencyVector: true}
	engine := newTestRouter(service, health)
	request := httptest.NewRequest(http.MethodPost, "/simulate", bytes.NewBufferString(`{"intent":"project_qa"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.simulateCalls != 1 || service.userID != "alice" {
		t.Fatalf("simulation escaped server-owned context: code=%d calls=%d user=%q body=%s", response.Code, service.simulateCalls, service.userID, response.Body.String())
	}
	var body SimulationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.AffectsLiveTraffic || body.Mode != "shadow_only" || body.Selection.Bucket != 42 || !body.Dependencies[policydomain.DependencyVector] {
		t.Fatalf("unexpected simulation response: %+v", body)
	}
}

func TestSimulateRejectsUnknownFieldsAndUnsupportedIntent(t *testing.T) {
	service := &fakeService{snapshot: policySnapshot()}
	engine := newTestRouter(service, fakeHealth{})
	for _, body := range []string{`{"intent":"general","weight":10000}`, `{"intent":"admin_override"}`} {
		request := httptest.NewRequest(http.MethodPost, "/simulate", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("untrusted simulation input was accepted: %d %s", response.Code, response.Body.String())
		}
	}
	if service.simulateCalls != 0 {
		t.Fatalf("invalid request reached strategy service %d times", service.simulateCalls)
	}
}
