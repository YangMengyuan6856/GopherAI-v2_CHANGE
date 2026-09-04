package session

import (
	"GopherAI/internal/app"
	"GopherAI/internal/contract"
	intentdomain "GopherAI/internal/intent"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type fakeChatApplication struct{}

type capturingChatApplication struct {
	input app.ChatInput
}

type cancellableStreamingApplication struct {
	started chan struct{}
	stopped chan struct{}
}

func (application *capturingChatApplication) Chat(_ context.Context, input app.ChatInput) (app.ChatOutput, error) {
	application.input = input
	return testOutput(input), nil
}

func (application *capturingChatApplication) Stream(_ context.Context, input app.ChatInput, _ app.StreamEmitter) (app.ChatOutput, error) {
	application.input = input
	return testOutput(input), nil
}

func (application *cancellableStreamingApplication) Chat(_ context.Context, input app.ChatInput) (app.ChatOutput, error) {
	return testOutput(input), nil
}

func (application *cancellableStreamingApplication) Stream(ctx context.Context, input app.ChatInput, _ app.StreamEmitter) (app.ChatOutput, error) {
	close(application.started)
	defer close(application.stopped)
	<-ctx.Done()
	return testOutput(input), ctx.Err()
}

func (fakeChatApplication) Chat(_ context.Context, input app.ChatInput) (app.ChatOutput, error) {
	return testOutput(input), nil
}

func (fakeChatApplication) Stream(_ context.Context, input app.ChatInput, emit app.StreamEmitter) (app.ChatOutput, error) {
	output := testOutput(input)
	if err := emit(contract.StreamEvent{Type: contract.StreamEventMeta, SchemaVersion: "1", TraceID: output.Request.TraceID, RequestID: output.Request.RequestID, SessionID: output.Result.SessionID}); err != nil {
		return output, err
	}
	if err := emit(contract.StreamEvent{Type: contract.StreamEventDelta, SchemaVersion: "1", TraceID: output.Request.TraceID, RequestID: output.Request.RequestID, SessionID: output.Result.SessionID, Text: "answer"}); err != nil {
		return output, err
	}
	return output, nil
}

func testOutput(input app.ChatInput) app.ChatOutput {
	shadow := intentdomain.CascadeDecision{
		Result:      contract.IntentResult{Intent: intentdomain.Troubleshooting, Confidence: .98, Version: intentdomain.CascadeVersion},
		Diagnostics: intentdomain.CascadeDiagnostics{FinalStage: "pattern", LatencyMillis: 1},
	}
	return app.ChatOutput{
		Request:      contract.RequestContext{TraceID: firstNonEmpty(input.TraceID, "trace-1"), RequestID: firstNonEmpty(input.RequestID, "request-1")},
		Intent:       contract.IntentResult{Intent: "legacy"},
		Decision:     contract.StrategyDecision{StrategyName: "legacy_chat", PolicyVersion: "policy-v0"},
		Result:       contract.AgentResult{SessionID: "session-1", Answer: "answer", Confidence: 1},
		ShadowIntent: &shadow,
	}
}

func TestAutoChatDoesNotRequireModelType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("userName", "user")
	context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/chat/auto", bytes.NewBufferString(`{"message":"question"}`))
	context.Request.Header.Set("Content-Type", "application/json")
	NewAutoHandler(fakeChatApplication{}).Chat(context)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response AutoChatResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Message != "answer" || response.Strategy != "legacy_chat" || response.SessionID != "session-1" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if response.IntentShadow == nil || response.IntentShadow.Intent != intentdomain.Troubleshooting || response.IntentShadow.Mode != "shadow" {
		t.Fatalf("shadow intent missing from response: %#v", response.IntentShadow)
	}
}

func TestAutoChatPropagatesExplicitKnowledgeRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	application := new(capturingChatApplication)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("userName", "user")
	context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/chat/auto", bytes.NewBufferString(`{"message":"question","knowledge_required":true}`))
	context.Request.Header.Set("Content-Type", "application/json")
	NewAutoHandler(application).Chat(context)
	if recorder.Code != http.StatusOK || !application.input.KnowledgeRequired {
		t.Fatalf("knowledge request was not propagated: code=%d input=%+v", recorder.Code, application.input)
	}
}

func TestAutoStreamUsesNamedSSEEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("userName", "user")
	context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/chat/auto/stream", bytes.NewBufferString(`{"message":"question"}`))
	context.Request.Header.Set("Content-Type", "application/json")
	NewAutoHandler(fakeChatApplication{}).Stream(context)
	body := recorder.Body.String()
	if !strings.Contains(body, "event: meta") || !strings.Contains(body, "event: delta") {
		t.Fatalf("missing named SSE events: %s", body)
	}
}

func TestAutoStreamDisconnectCancelsDownstreamWorkWithoutWritingAnErrorEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	application := &cancellableStreamingApplication{started: make(chan struct{}), stopped: make(chan struct{})}
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Set("userName", "user")
	requestContext, cancel := context.WithCancel(context.Background())
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/chat/auto/stream", bytes.NewBufferString(`{"message":"question"}`)).WithContext(requestContext)
	ginContext.Request.Header.Set("Content-Type", "application/json")

	handlerReturned := make(chan struct{})
	go func() {
		NewAutoHandler(application).Stream(ginContext)
		close(handlerReturned)
	}()
	awaitSignal(t, application.started, "stream application did not start")
	cancel()
	awaitSignal(t, application.stopped, "stream application did not stop after disconnect")
	awaitSignal(t, handlerReturned, "stream handler did not return after disconnect")

	if strings.Contains(recorder.Body.String(), "event: error") {
		t.Fatalf("handler wrote an error event after the client connection ended: %s", recorder.Body.String())
	}
}

func awaitSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(failure)
	}
}
