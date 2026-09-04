package session

import (
	"GopherAI/internal/app"
	"GopherAI/internal/contract"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type fakeChatApplication struct{}

type capturingChatApplication struct {
	input app.ChatInput
}

func (application *capturingChatApplication) Chat(_ context.Context, input app.ChatInput) (app.ChatOutput, error) {
	application.input = input
	return testOutput(input), nil
}

func (application *capturingChatApplication) Stream(_ context.Context, input app.ChatInput, _ app.StreamEmitter) (app.ChatOutput, error) {
	application.input = input
	return testOutput(input), nil
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
	return app.ChatOutput{
		Request:  contract.RequestContext{TraceID: firstNonEmpty(input.TraceID, "trace-1"), RequestID: firstNonEmpty(input.RequestID, "request-1")},
		Intent:   contract.IntentResult{Intent: "legacy"},
		Decision: contract.StrategyDecision{StrategyName: "legacy_chat", PolicyVersion: "policy-v0"},
		Result:   contract.AgentResult{SessionID: "session-1", Answer: "answer", Confidence: 1},
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
