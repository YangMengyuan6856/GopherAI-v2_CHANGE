package app

import (
	"GopherAI/internal/contract"
	"context"
	"errors"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (clock *fakeClock) Now() time.Time {
	current := clock.now
	clock.now = clock.now.Add(time.Millisecond)
	return current
}

type fakeIDs struct{ values []string }

func (ids *fakeIDs) NewID() string {
	value := ids.values[0]
	ids.values = ids.values[1:]
	return value
}

type fakeSelector struct{ decision contract.StrategyDecision }

func (selector fakeSelector) Select(context.Context, contract.RequestContext, contract.IntentResult) (contract.StrategyDecision, error) {
	return selector.decision, nil
}

type fakeStrategy struct{ fail bool }

func (fakeStrategy) Name() string { return "legacy_chat" }
func (strategy fakeStrategy) Execute(_ context.Context, request contract.RequestContext) (contract.AgentResult, error) {
	if strategy.fail {
		return contract.AgentResult{}, errors.New("model secret")
	}
	return contract.AgentResult{SessionID: "session-1", Answer: "answer", Resolved: true}, nil
}
func (strategy fakeStrategy) Stream(_ context.Context, request contract.RequestContext, emit StreamEmitter) (contract.AgentResult, error) {
	if err := emit(contract.StreamEvent{Type: contract.StreamEventMeta, SessionID: "session-1"}); err != nil {
		return contract.AgentResult{}, err
	}
	if err := emit(contract.StreamEvent{Type: contract.StreamEventDelta, SessionID: "session-1", Text: "answer"}); err != nil {
		return contract.AgentResult{}, err
	}
	return contract.AgentResult{SessionID: "session-1", Answer: "answer", Resolved: true}, nil
}

func testService(t *testing.T, strategy ChatStrategy) *Service {
	t.Helper()
	budgets := defaultBudgets
	selector := fakeSelector{decision: contract.StrategyDecision{
		StrategyName: "legacy_chat", StrategyVersion: "legacy-v0", PolicyVersion: "policy-v0",
		ReasonCode: "fixed", Budgets: budgets,
	}}
	service, err := NewService(selector, &fakeClock{now: time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)}, &fakeIDs{values: []string{"request-1", "trace-1"}}, strategy)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestChatRunsFixedStrategyAndBuildsTrace(t *testing.T) {
	service := testService(t, fakeStrategy{})
	output, err := service.Chat(context.Background(), ChatInput{UserID: "user", TenantID: "user", Question: "question"})
	if err != nil {
		t.Fatal(err)
	}
	if output.Result.Answer != "answer" || output.Request.PolicyVersion != "policy-v0" {
		t.Fatalf("unexpected output: %#v", output)
	}
	if output.Trace.TraceID != "trace-1" || len(output.Trace.Steps) != 2 {
		t.Fatalf("trace was not completed: %#v", output.Trace)
	}
}

func TestStreamEnrichesEveryEventAndEndsWithFinal(t *testing.T) {
	service := testService(t, fakeStrategy{})
	var events []contract.StreamEvent
	_, err := service.Stream(context.Background(), ChatInput{UserID: "user", TenantID: "user", Question: "question"}, func(event contract.StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[2].Type != contract.StreamEventFinal {
		t.Fatalf("unexpected events: %#v", events)
	}
	for _, event := range events {
		if event.TraceID != "trace-1" || event.Strategy != "legacy_chat" || event.SchemaVersion != "1" {
			t.Fatalf("event was not enriched: %#v", event)
		}
	}
}

func TestChatMapsUnknownFailureToSafeDomainError(t *testing.T) {
	service := testService(t, fakeStrategy{fail: true})
	_, err := service.Chat(context.Background(), ChatInput{UserID: "user", TenantID: "user", Question: "question"})
	domainError, ok := err.(*contract.DomainError)
	if !ok || domainError.Code != "INTERNAL_ERROR" || domainError.TraceID != "trace-1" {
		t.Fatalf("unexpected error: %#v", err)
	}
	if domainError.Message == "model secret" {
		t.Fatal("internal failure leaked through public message")
	}
}
