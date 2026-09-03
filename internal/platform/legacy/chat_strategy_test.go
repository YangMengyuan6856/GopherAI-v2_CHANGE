package legacy

import (
	"GopherAI/internal/app"
	"GopherAI/internal/contract"
	"context"
	"errors"
	"testing"
)

type fakeBackend struct{ fail bool }

func (backend fakeBackend) Chat(context.Context, string, string, string) (string, string, error) {
	if backend.fail {
		return "session-1", "", errors.New("provider details")
	}
	return "session-1", "answer", nil
}

func (backend fakeBackend) Stream(_ context.Context, _ string, _ string, _ string, onSession func(string) error, onDelta func(string) error) (string, error) {
	if err := onSession("session-1"); err != nil {
		return "session-1", err
	}
	if err := onDelta("answer"); err != nil {
		return "session-1", err
	}
	return "session-1", nil
}

func TestChatStrategyAdaptsLegacyOutput(t *testing.T) {
	result, err := NewChatStrategy(fakeBackend{}).Execute(context.Background(), contract.RequestContext{UserID: "user", Question: "question"})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID != "session-1" || result.Answer != "answer" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestChatStrategyStreamsTypedMetaAndDelta(t *testing.T) {
	var events []contract.StreamEvent
	_, err := NewChatStrategy(fakeBackend{}).Stream(context.Background(), contract.RequestContext{UserID: "user", Question: "question"}, app.StreamEmitter(func(event contract.StreamEvent) error {
		events = append(events, event)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != contract.StreamEventMeta || events[1].Type != contract.StreamEventDelta {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestChatStrategyMapsProviderFailure(t *testing.T) {
	_, err := NewChatStrategy(fakeBackend{fail: true}).Execute(context.Background(), contract.RequestContext{})
	domainError, ok := err.(*contract.DomainError)
	if !ok || domainError.Code != "LEGACY_MODEL_ERROR" || domainError.Message == "provider details" {
		t.Fatalf("unexpected error: %#v", err)
	}
}
