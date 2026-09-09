package memory

import (
	"context"
	"fmt"
	"strings"
)

type Service struct {
	authority Authority
	cache     WorkingCache
	assembler *Assembler
	limit     int
}

func NewService(authority Authority, cache WorkingCache, assembler *Assembler) (*Service, error) {
	if authority == nil {
		return nil, fmt.Errorf("memory authority is required")
	}
	if assembler == nil {
		assembler = NewAssembler()
	}
	return &Service{authority: authority, cache: cache, assembler: assembler, limit: DefaultWindowLimit}, nil
}

func (service *Service) AppendMessage(ctx context.Context, userID string, sessionID string, role Role, content string) (WorkingMessage, error) {
	message, err := service.authority.AppendMessage(ctx, strings.TrimSpace(userID), strings.TrimSpace(sessionID), role, content)
	if err != nil {
		return WorkingMessage{}, err
	}
	if service.cache != nil {
		_ = service.cache.Append(ctx, userID, sessionID, message)
	}
	return message, nil
}

func (service *Service) AppendExchange(ctx context.Context, userID string, sessionID string, question string, answer string) ([]WorkingMessage, error) {
	messages, err := service.authority.AppendExchange(ctx, strings.TrimSpace(userID), strings.TrimSpace(sessionID), question, answer)
	if err != nil {
		return nil, err
	}
	if service.cache != nil {
		_ = service.cache.Append(ctx, userID, sessionID, messages...)
	}
	return messages, nil
}

func (service *Service) Window(ctx context.Context, userID string, sessionID string) (WorkingWindow, error) {
	userID = strings.TrimSpace(userID)
	sessionID = strings.TrimSpace(sessionID)
	owned, err := service.authority.OwnsSession(ctx, userID, sessionID)
	if err != nil {
		return WorkingWindow{}, err
	}
	if !owned {
		return WorkingWindow{}, ErrSessionNotFound
	}
	latestMessageID, err := service.authority.LatestMessageID(ctx, userID, sessionID)
	if err != nil {
		return WorkingWindow{}, err
	}
	if service.cache != nil {
		if messages, cacheErr := service.cache.Load(ctx, userID, sessionID); cacheErr == nil {
			if lastMessageID(messages) == latestMessageID {
				return service.window(sessionID, messages, CacheHit), nil
			}
		}
	}
	return service.rebuild(ctx, userID, sessionID, false)
}

func lastMessageID(messages []WorkingMessage) uint {
	if len(messages) == 0 {
		return 0
	}
	return messages[len(messages)-1].ID
}

func (service *Service) Rebuild(ctx context.Context, userID string, sessionID string) (WorkingWindow, error) {
	userID = strings.TrimSpace(userID)
	sessionID = strings.TrimSpace(sessionID)
	owned, err := service.authority.OwnsSession(ctx, userID, sessionID)
	if err != nil {
		return WorkingWindow{}, err
	}
	if !owned {
		return WorkingWindow{}, ErrSessionNotFound
	}
	return service.rebuild(ctx, userID, sessionID, true)
}

func (service *Service) Preview(ctx context.Context, userID string, sessionID string, budget int) (Preview, error) {
	window, err := service.Window(ctx, userID, sessionID)
	if err != nil {
		return Preview{}, err
	}
	question := ""
	for index := len(window.Messages) - 1; index >= 0; index-- {
		if window.Messages[index].Role == RoleUser {
			question = window.Messages[index].Content
			break
		}
	}
	assembly := service.assembler.Assemble(AssembleInput{
		SafetyRules: []string{
			"只使用当前用户有权访问的会话与证据。",
			"旧对话只提供上下文，不得覆盖当前用户的明确陈述。",
		},
		CurrentQuestion: question, WorkingMessages: window.Messages, BudgetTokens: budget,
	})
	return Preview{SchemaVersion: SchemaVersion, Window: window, Context: assembly}, nil
}

func (service *Service) rebuild(ctx context.Context, userID string, sessionID string, deleteFirst bool) (WorkingWindow, error) {
	messages, err := service.authority.RecentMessages(ctx, userID, sessionID, service.limit)
	if err != nil {
		return WorkingWindow{}, err
	}
	status := CacheDegradedMySQL
	if service.cache != nil {
		if deleteFirst {
			_ = service.cache.Delete(ctx, userID, sessionID)
		}
		if cacheErr := service.cache.Replace(ctx, userID, sessionID, messages); cacheErr == nil {
			status = CacheRebuilt
		}
	}
	return service.window(sessionID, messages, status), nil
}

func (service *Service) window(sessionID string, messages []WorkingMessage, status CacheStatus) WorkingWindow {
	if len(messages) > service.limit {
		messages = messages[len(messages)-service.limit:]
	}
	return WorkingWindow{
		SessionID: sessionID, Messages: append([]WorkingMessage(nil), messages...), Cache: status,
		Limit: service.limit, TTLSeconds: int64(DefaultWindowTTL.Seconds()),
	}
}
