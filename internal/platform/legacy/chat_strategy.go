package legacy

import (
	"GopherAI/common/code"
	"GopherAI/internal/app"
	"GopherAI/internal/contract"
	"GopherAI/internal/policy"
	sessionservice "GopherAI/service/session"
	"context"
	"fmt"
)

const directModelType = "1"

type ChatBackend interface {
	Chat(ctx context.Context, userID string, sessionID string, question string) (resolvedSessionID string, answer string, err error)
	Stream(ctx context.Context, userID string, sessionID string, question string, onSession func(string) error, onDelta func(string) error) (resolvedSessionID string, err error)
}

type ChatStrategy struct {
	backend ChatBackend
}

func NewChatStrategy(backend ChatBackend) *ChatStrategy {
	return &ChatStrategy{backend: backend}
}

func NewDefaultChatStrategy() *ChatStrategy {
	return NewChatStrategy(SessionBackend{})
}

func (*ChatStrategy) Name() string { return policy.LegacyChatStrategyName }

func (strategy *ChatStrategy) Execute(ctx context.Context, request contract.RequestContext) (contract.AgentResult, error) {
	sessionID, answer, err := strategy.backend.Chat(ctx, request.UserID, request.SessionID, request.Question)
	if err != nil {
		return contract.AgentResult{SessionID: sessionID}, legacyError(err)
	}
	return contract.AgentResult{SessionID: sessionID, Answer: answer}, nil
}

func (strategy *ChatStrategy) Stream(ctx context.Context, request contract.RequestContext, emit app.StreamEmitter) (contract.AgentResult, error) {
	sessionID, err := strategy.backend.Stream(ctx, request.UserID, request.SessionID, request.Question,
		func(resolvedSessionID string) error {
			return emit(contract.StreamEvent{Type: contract.StreamEventMeta, SessionID: resolvedSessionID})
		},
		func(text string) error {
			return emit(contract.StreamEvent{Type: contract.StreamEventDelta, SessionID: request.SessionID, Text: text})
		},
	)
	if err != nil {
		return contract.AgentResult{SessionID: sessionID}, legacyError(err)
	}
	return contract.AgentResult{SessionID: sessionID}, nil
}

func legacyError(err error) *contract.DomainError {
	return contract.NewDomainError("LEGACY_MODEL_ERROR", contract.ErrorModel, "模型暂时不可用，请稍后重试", true, err)
}

type SessionBackend struct{}

var _ app.ChatStrategy = (*ChatStrategy)(nil)

func (SessionBackend) Chat(ctx context.Context, userID string, sessionID string, question string) (string, string, error) {
	if sessionID == "" {
		createdSessionID, answer, resultCode := sessionservice.CreateSessionAndSendMessageContext(ctx, userID, question, directModelType)
		if resultCode != code.CodeSuccess {
			return createdSessionID, "", fmt.Errorf("legacy create chat failed with code %d", resultCode)
		}
		return createdSessionID, answer, nil
	}
	answer, resultCode := sessionservice.ChatSendContext(ctx, userID, sessionID, question, directModelType)
	if resultCode != code.CodeSuccess {
		return sessionID, "", fmt.Errorf("legacy chat failed with code %d", resultCode)
	}
	return sessionID, answer, nil
}

func (SessionBackend) Stream(ctx context.Context, userID string, sessionID string, question string, onSession func(string) error, onDelta func(string) error) (string, error) {
	resolvedSessionID := sessionID
	if resolvedSessionID == "" {
		var resultCode code.Code
		resolvedSessionID, resultCode = sessionservice.CreateStreamSessionOnly(userID, question)
		if resultCode != code.CodeSuccess {
			return "", fmt.Errorf("legacy stream session creation failed with code %d", resultCode)
		}
	}
	if err := onSession(resolvedSessionID); err != nil {
		return resolvedSessionID, err
	}
	resultCode := sessionservice.StreamMessageToExistingSessionContext(ctx, userID, resolvedSessionID, question, directModelType, onDelta)
	if resultCode != code.CodeSuccess {
		return resolvedSessionID, fmt.Errorf("legacy stream failed with code %d", resultCode)
	}
	return resolvedSessionID, nil
}
