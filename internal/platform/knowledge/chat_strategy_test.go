package knowledge

import (
	knowledgeagent "GopherAI/internal/agent/knowledge"
	"GopherAI/internal/app"
	"GopherAI/internal/contract"
	"GopherAI/internal/rag"
	"context"
	"errors"
	"testing"
)

type fakeAnswerer struct {
	input  knowledgeagent.Input
	output knowledgeagent.Output
	err    error
}

func (answerer *fakeAnswerer) Answer(_ context.Context, input knowledgeagent.Input) (knowledgeagent.Output, error) {
	answerer.input = input
	return answerer.output, answerer.err
}

type fakeConversationStore struct {
	sessionID string
	saved     []string
	err       error
}

func (store *fakeConversationStore) ResolveSession(context.Context, string, string, string) (string, error) {
	return store.sessionID, store.err
}

func (store *fakeConversationStore) SaveExchange(_ context.Context, userID string, sessionID string, question string, answer string) error {
	store.saved = []string{userID, sessionID, question, answer}
	return store.err
}

func TestChatStrategyExecutesKnowledgeAgentAndPersistsExchange(t *testing.T) {
	answerer := &fakeAnswerer{output: knowledgeagent.Output{Result: contract.AgentResult{
		Answer: "回答 [1]", Resolved: true, Citations: []contract.Citation{{ID: "C1"}},
	}}}
	store := &fakeConversationStore{sessionID: "session-1"}
	strategy, err := NewChatStrategy(answerer, store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := strategy.Execute(context.Background(), contract.RequestContext{TenantID: "tenant-a", UserID: "user-a", Question: "问题"})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID != "session-1" || answerer.input.TenantID != "tenant-a" || len(store.saved) != 4 || store.saved[3] != "回答 [1]" {
		t.Fatalf("unexpected execute result=%+v input=%+v saved=%+v", result, answerer.input, store.saved)
	}
}

func TestChatStrategyStreamsTypedAnswerAndCitations(t *testing.T) {
	answerer := &fakeAnswerer{output: knowledgeagent.Output{Result: contract.AgentResult{
		Answer: "回答 [1]", Resolved: true, Citations: []contract.Citation{{ID: "C1", EvidenceID: "chunk-1"}},
	}}}
	strategy, _ := NewChatStrategy(answerer, &fakeConversationStore{sessionID: "session-1"})
	var events []contract.StreamEvent
	result, err := strategy.Stream(context.Background(), contract.RequestContext{TenantID: "tenant-a", UserID: "user-a", Question: "问题"}, app.StreamEmitter(func(event contract.StreamEvent) error {
		events = append(events, event)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Type != contract.StreamEventMeta || events[1].Type != contract.StreamEventDelta || events[2].Type != contract.StreamEventCitation || result.SessionID != "session-1" {
		t.Fatalf("unexpected stream events=%+v result=%+v", events, result)
	}
}

func TestChatStrategyMapsUnverifiedModelOutputToSafeError(t *testing.T) {
	strategy, _ := NewChatStrategy(&fakeAnswerer{err: knowledgeagent.ErrModelOutput}, &fakeConversationStore{sessionID: "session-1"})
	_, err := strategy.Execute(context.Background(), contract.RequestContext{TenantID: "tenant-a", UserID: "user-a", Question: "问题"})
	var domainError *contract.DomainError
	if !errors.As(err, &domainError) || domainError.Code != "KNOWLEDGE_ANSWER_UNVERIFIED" {
		t.Fatalf("unexpected mapped error: %v", err)
	}
}

func TestChatStrategyKeepsInsufficientEvidenceAsControlledAnswer(t *testing.T) {
	answerer := &fakeAnswerer{output: knowledgeagent.Output{
		Result: contract.AgentResult{Answer: "证据不足，未调用模型。", NeedsUserInput: true},
		Gate:   rag.EvidenceGateResult{ReasonCode: rag.GateReasonNoEvidence},
	}}
	strategy, _ := NewChatStrategy(answerer, &fakeConversationStore{sessionID: "session-1"})
	result, err := strategy.Execute(context.Background(), contract.RequestContext{TenantID: "tenant-a", UserID: "user-a", Question: "问题"})
	if err != nil || result.Resolved || !result.NeedsUserInput {
		t.Fatalf("insufficient evidence must remain a normal result: result=%+v err=%v", result, err)
	}
}
