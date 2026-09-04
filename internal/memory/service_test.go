package memory

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeAuthority struct {
	owned       bool
	messages    []WorkingMessage
	recentCalls int
	appendCalls int
}

func (authority *fakeAuthority) OwnsSession(context.Context, string, string) (bool, error) {
	return authority.owned, nil
}

func (authority *fakeAuthority) LatestMessageID(context.Context, string, string) (uint, error) {
	if len(authority.messages) == 0 {
		return 0, nil
	}
	return authority.messages[len(authority.messages)-1].ID, nil
}

func (authority *fakeAuthority) AppendMessage(_ context.Context, _ string, _ string, role Role, content string) (WorkingMessage, error) {
	authority.appendCalls++
	message := WorkingMessage{ID: uint(len(authority.messages) + 1), Role: role, Content: content, CreatedAt: time.Now().UTC()}
	authority.messages = append(authority.messages, message)
	return message, nil
}

func (authority *fakeAuthority) AppendExchange(_ context.Context, _ string, _ string, question string, answer string) ([]WorkingMessage, error) {
	authority.appendCalls++
	messages := []WorkingMessage{{ID: 1, Role: RoleUser, Content: question}, {ID: 2, Role: RoleAssistant, Content: answer}}
	authority.messages = append(authority.messages, messages...)
	return messages, nil
}

func (authority *fakeAuthority) RecentMessages(context.Context, string, string, int) ([]WorkingMessage, error) {
	authority.recentCalls++
	return append([]WorkingMessage(nil), authority.messages...), nil
}

type fakeCache struct {
	messages     []WorkingMessage
	loadErr      error
	writeErr     error
	appendCalls  int
	replaceCalls int
	deleteCalls  int
}

func (cache *fakeCache) Load(context.Context, string, string) ([]WorkingMessage, error) {
	return append([]WorkingMessage(nil), cache.messages...), cache.loadErr
}

func (cache *fakeCache) Append(_ context.Context, _ string, _ string, messages ...WorkingMessage) error {
	cache.appendCalls++
	if cache.writeErr == nil {
		cache.messages = append(cache.messages, messages...)
	}
	return cache.writeErr
}

func (cache *fakeCache) Replace(_ context.Context, _ string, _ string, messages []WorkingMessage) error {
	cache.replaceCalls++
	if cache.writeErr == nil {
		cache.messages = append([]WorkingMessage(nil), messages...)
	}
	return cache.writeErr
}

func (cache *fakeCache) Delete(context.Context, string, string) error {
	cache.deleteCalls++
	cache.messages = nil
	return cache.writeErr
}

func TestWindowUsesRedisAfterOwnershipAndFreshnessCheck(t *testing.T) {
	authority := &fakeAuthority{owned: true, messages: []WorkingMessage{{ID: 2, Role: RoleAssistant, Content: "durable"}}}
	cache := &fakeCache{messages: []WorkingMessage{{ID: 2, Role: RoleAssistant, Content: "cached"}}}
	service, _ := NewService(authority, cache, nil)
	window, err := service.Window(context.Background(), "alice", "session-1")
	if err != nil || window.Cache != CacheHit || authority.recentCalls != 0 || len(window.Messages) != 1 {
		t.Fatalf("unexpected cache hit: window=%+v authority=%+v err=%v", window, authority, err)
	}
}

func TestStaleRedisWindowIsRebuiltFromMySQL(t *testing.T) {
	authority := &fakeAuthority{owned: true, messages: []WorkingMessage{
		{ID: 1, Role: RoleUser, Content: "first"}, {ID: 2, Role: RoleAssistant, Content: "latest"},
	}}
	cache := &fakeCache{messages: []WorkingMessage{{ID: 1, Role: RoleUser, Content: "first"}}}
	service, _ := NewService(authority, cache, nil)
	window, err := service.Window(context.Background(), "alice", "session-1")
	if err != nil || window.Cache != CacheRebuilt || len(window.Messages) != 2 || window.Messages[1].ID != 2 {
		t.Fatalf("stale cache was trusted: window=%+v err=%v", window, err)
	}
}

func TestWindowRebuildsRedisFromMySQLInOrder(t *testing.T) {
	authority := &fakeAuthority{owned: true, messages: []WorkingMessage{
		{ID: 1, Role: RoleUser, Content: "first"}, {ID: 2, Role: RoleAssistant, Content: "second"},
	}}
	cache := &fakeCache{loadErr: ErrCacheMiss}
	service, _ := NewService(authority, cache, nil)
	window, err := service.Window(context.Background(), "alice", "session-1")
	if err != nil || window.Cache != CacheRebuilt || authority.recentCalls != 1 || cache.replaceCalls != 1 {
		t.Fatalf("unexpected rebuild: window=%+v authority=%+v cache=%+v err=%v", window, authority, cache, err)
	}
	if window.Messages[0].Content != "first" || window.Messages[1].Content != "second" {
		t.Fatalf("rebuild changed message order: %+v", window.Messages)
	}
}

func TestWindowFallsBackToMySQLWhenRedisIsUnavailable(t *testing.T) {
	authority := &fakeAuthority{owned: true, messages: []WorkingMessage{{ID: 1, Role: RoleUser, Content: "durable"}}}
	cache := &fakeCache{loadErr: errors.New("redis down"), writeErr: errors.New("redis down")}
	service, _ := NewService(authority, cache, nil)
	window, err := service.Window(context.Background(), "alice", "session-1")
	if err != nil || window.Cache != CacheDegradedMySQL || len(window.Messages) != 1 {
		t.Fatalf("mysql fallback failed: window=%+v err=%v", window, err)
	}
}

func TestCrossUserSessionIsHiddenBeforeCacheAccess(t *testing.T) {
	authority := &fakeAuthority{owned: false}
	cache := &fakeCache{messages: []WorkingMessage{{ID: 1, Role: RoleUser, Content: "secret"}}}
	service, _ := NewService(authority, cache, nil)
	_, err := service.Window(context.Background(), "mallory", "session-1")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected hidden session, got %v", err)
	}
}

func TestAppendIsSuccessfulAfterDurableWriteEvenIfCacheFails(t *testing.T) {
	authority := &fakeAuthority{owned: true}
	cache := &fakeCache{writeErr: errors.New("redis down")}
	service, _ := NewService(authority, cache, nil)
	message, err := service.AppendMessage(context.Background(), "alice", "session-1", RoleUser, "durable")
	if err != nil || message.Content != "durable" || authority.appendCalls != 1 || cache.appendCalls != 1 {
		t.Fatalf("durable append incorrectly depended on cache: message=%+v authority=%+v cache=%+v err=%v", message, authority, cache, err)
	}
}

func TestExplicitRebuildDeletesOnlyScopedCacheThenRestoresIt(t *testing.T) {
	authority := &fakeAuthority{owned: true, messages: []WorkingMessage{{ID: 1, Role: RoleUser, Content: "durable"}}}
	cache := new(fakeCache)
	service, _ := NewService(authority, cache, nil)
	window, err := service.Rebuild(context.Background(), "alice", "session-1")
	if err != nil || window.Cache != CacheRebuilt || cache.deleteCalls != 1 || cache.replaceCalls != 1 {
		t.Fatalf("explicit rebuild failed: window=%+v cache=%+v err=%v", window, cache, err)
	}
}
