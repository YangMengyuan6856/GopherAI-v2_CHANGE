package agentrun

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/diagnostic"
	"GopherAI/middleware/requestid"

	"github.com/gin-gonic/gin"
)

type fakeCaseStrategy struct {
	result   diagnostic.CaseStrategyResult
	err      error
	calls    int
	tenantID string
	userID   string
}

func (strategy *fakeCaseStrategy) Analyze(_ context.Context, tenantID string, userID string, _ string) (diagnostic.CaseStrategyResult, error) {
	strategy.calls++
	strategy.tenantID, strategy.userID = tenantID, userID
	return strategy.result, strategy.err
}

type fakeCaseObserver struct {
	strength string
	outcome  string
	calls    int
}

func (observer *fakeCaseObserver) RecordCaseStrategy(strength string, outcome string, _ time.Duration) {
	observer.calls++
	observer.strength, observer.outcome = strength, outcome
}

func newCaseShadowRouter(strategy CaseStrategy, observer CaseStrategyObserver) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(requestid.Attach(), func(ctx *gin.Context) { ctx.Set("userName", "alice"); ctx.Next() })
	engine.POST("/case-shadow", NewCaseShadowHandler(strategy, observer).Analyze)
	return engine
}

func TestCaseShadowUsesAuthenticatedPrincipalAndReportsBoundary(t *testing.T) {
	strategy := &fakeCaseStrategy{result: diagnostic.CaseStrategyResult{
		SchemaVersion: diagnostic.CaseStrategySchemaVersion, Strategy: diagnostic.CaseBasedStrategyName,
		CaseStrength: diagnostic.CaseStrengthStrong, AffectsLiveTraffic: false, Mode: "shadow_only",
	}}
	observer := new(fakeCaseObserver)
	engine := newCaseShadowRouter(strategy, observer)
	request := httptest.NewRequest(http.MethodPost, "/case-shadow", bytes.NewBufferString(`{"message":"Redis NOAUTH"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strategy.calls != 1 || strategy.tenantID != "alice" || strategy.userID != "alice" {
		t.Fatalf("case shadow escaped authenticated scope: code=%d strategy=%+v body=%s", response.Code, strategy, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"affects_live_traffic":false`) || !strings.Contains(response.Body.String(), `"mode":"shadow_only"`) || observer.calls != 1 || observer.strength != "strong" || observer.outcome != "success" {
		t.Fatalf("shadow boundary or metric missing: observer=%+v body=%s", observer, response.Body.String())
	}
}

func TestCaseShadowRejectsUnknownFieldsBeforeStrategy(t *testing.T) {
	strategy := new(fakeCaseStrategy)
	observer := new(fakeCaseObserver)
	engine := newCaseShadowRouter(strategy, observer)
	request := httptest.NewRequest(http.MethodPost, "/case-shadow", bytes.NewBufferString(`{"message":"Redis NOAUTH","permission":"admin"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || strategy.calls != 0 || observer.outcome != "error" {
		t.Fatalf("unknown field reached strategy: code=%d calls=%d body=%s", response.Code, strategy.calls, response.Body.String())
	}
}

func TestCaseShadowMapsCancellationWithoutReturningSuccess(t *testing.T) {
	strategy := &fakeCaseStrategy{err: context.Canceled}
	observer := new(fakeCaseObserver)
	engine := newCaseShadowRouter(strategy, observer)
	request := httptest.NewRequest(http.MethodPost, "/case-shadow", bytes.NewBufferString(`{"message":"Redis NOAUTH"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != 499 || !errors.Is(strategy.err, context.Canceled) || observer.outcome != "cancelled" {
		t.Fatalf("cancellation was masked: code=%d observer=%+v body=%s", response.Code, observer, response.Body.String())
	}
}
