package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"GopherAI/model"

	redisclient "github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Authority interface {
	OwnsSession(context.Context, string, string) (bool, error)
	LatestMessageID(context.Context, string, string) (uint, error)
	AppendMessage(context.Context, string, string, Role, string) (WorkingMessage, error)
	AppendExchange(context.Context, string, string, string, string) ([]WorkingMessage, error)
	RecentMessages(context.Context, string, string, int) ([]WorkingMessage, error)
}

type WorkingCache interface {
	Load(context.Context, string, string) ([]WorkingMessage, error)
	Append(context.Context, string, string, ...WorkingMessage) error
	Replace(context.Context, string, string, []WorkingMessage) error
	Delete(context.Context, string, string) error
}

type GormAuthority struct {
	db *gorm.DB
}

func NewGormAuthority(db *gorm.DB) *GormAuthority { return &GormAuthority{db: db} }

func (authority *GormAuthority) OwnsSession(ctx context.Context, userID string, sessionID string) (bool, error) {
	if authority == nil || authority.db == nil {
		return false, gorm.ErrInvalidDB
	}
	var count int64
	err := authority.db.WithContext(ctx).Model(&model.Session{}).
		Where("id = ? AND user_name = ?", strings.TrimSpace(sessionID), strings.TrimSpace(userID)).Count(&count).Error
	return count == 1, err
}

func (authority *GormAuthority) LatestMessageID(ctx context.Context, userID string, sessionID string) (uint, error) {
	if authority == nil || authority.db == nil {
		return 0, gorm.ErrInvalidDB
	}
	var latest struct {
		ID uint
	}
	err := authority.db.WithContext(ctx).Model(&model.Message{}).
		Select("COALESCE(MAX(id), 0) AS id").
		Where("session_id = ? AND user_name = ?", strings.TrimSpace(sessionID), strings.TrimSpace(userID)).Scan(&latest).Error
	return latest.ID, err
}

func (authority *GormAuthority) AppendMessage(ctx context.Context, userID string, sessionID string, role Role, content string) (WorkingMessage, error) {
	if authority == nil || authority.db == nil {
		return WorkingMessage{}, gorm.ErrInvalidDB
	}
	message := model.Message{SessionID: strings.TrimSpace(sessionID), UserName: strings.TrimSpace(userID), Content: strings.TrimSpace(content), IsUser: role == RoleUser}
	working := messageFromModel(message)
	if err := working.Validate(); err != nil {
		return WorkingMessage{}, err
	}
	err := authority.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.Session{}).Where("id = ? AND user_name = ?", message.SessionID, message.UserName).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return ErrSessionNotFound
		}
		if err := tx.Create(&message).Error; err != nil {
			return err
		}
		return tx.Model(&model.Session{}).Where("id = ? AND user_name = ?", message.SessionID, message.UserName).
			Update("updated_at", time.Now().UTC()).Error
	})
	return messageFromModel(message), err
}

func (authority *GormAuthority) AppendExchange(ctx context.Context, userID string, sessionID string, question string, answer string) ([]WorkingMessage, error) {
	if authority == nil || authority.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	userID = strings.TrimSpace(userID)
	sessionID = strings.TrimSpace(sessionID)
	messages := []model.Message{
		{SessionID: sessionID, UserName: userID, Content: strings.TrimSpace(question), IsUser: true},
		{SessionID: sessionID, UserName: userID, Content: strings.TrimSpace(answer), IsUser: false},
	}
	for _, message := range messages {
		if err := messageFromModel(message).Validate(); err != nil {
			return nil, err
		}
	}
	err := authority.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.Session{}).Where("id = ? AND user_name = ?", sessionID, userID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return ErrSessionNotFound
		}
		if err := tx.Create(&messages).Error; err != nil {
			return err
		}
		return tx.Model(&model.Session{}).Where("id = ? AND user_name = ?", sessionID, userID).
			Update("updated_at", time.Now().UTC()).Error
	})
	if err != nil {
		return nil, err
	}
	return []WorkingMessage{messageFromModel(messages[0]), messageFromModel(messages[1])}, nil
}

func (authority *GormAuthority) RecentMessages(ctx context.Context, userID string, sessionID string, limit int) ([]WorkingMessage, error) {
	if authority == nil || authority.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	owned, err := authority.OwnsSession(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	if !owned {
		return nil, ErrSessionNotFound
	}
	limit = boundedWindowLimit(limit)
	var persisted []model.Message
	if err := authority.db.WithContext(ctx).Where("session_id = ? AND user_name = ?", sessionID, userID).
		Order("id desc").Limit(limit).Find(&persisted).Error; err != nil {
		return nil, err
	}
	result := make([]WorkingMessage, 0, len(persisted))
	for index := len(persisted) - 1; index >= 0; index-- {
		result = append(result, messageFromModel(persisted[index]))
	}
	return result, nil
}

func messageFromModel(message model.Message) WorkingMessage {
	role := RoleAssistant
	if message.IsUser {
		role = RoleUser
	}
	return WorkingMessage{ID: message.ID, Role: role, Content: message.Content, CreatedAt: message.CreatedAt.UTC()}
}

type RedisWorkingCache struct {
	client *redisclient.Client
	limit  int
	ttl    time.Duration
}

func NewRedisWorkingCache(client *redisclient.Client, limit int, ttl time.Duration) *RedisWorkingCache {
	if limit <= 0 {
		limit = DefaultWindowLimit
	}
	if ttl <= 0 {
		ttl = DefaultWindowTTL
	}
	return &RedisWorkingCache{client: client, limit: limit, ttl: ttl}
}

func (cache *RedisWorkingCache) Load(ctx context.Context, userID string, sessionID string) ([]WorkingMessage, error) {
	if cache == nil || cache.client == nil {
		return nil, errors.New("working memory redis client is unavailable")
	}
	values, err := cache.client.LRange(ctx, cache.key(userID, sessionID), 0, -1).Result()
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, ErrCacheMiss
	}
	messages := make([]WorkingMessage, 0, len(values))
	for _, value := range values {
		var message WorkingMessage
		if err := json.Unmarshal([]byte(value), &message); err != nil {
			return nil, fmt.Errorf("decode working memory: %w", err)
		}
		if err := message.Validate(); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func (cache *RedisWorkingCache) Append(ctx context.Context, userID string, sessionID string, messages ...WorkingMessage) error {
	if cache == nil || cache.client == nil {
		return errors.New("working memory redis client is unavailable")
	}
	values := make([]interface{}, 0, len(messages))
	for _, message := range messages {
		if err := message.Validate(); err != nil {
			return err
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			return err
		}
		values = append(values, encoded)
	}
	if len(values) == 0 {
		return nil
	}
	key := cache.key(userID, sessionID)
	_, err := cache.client.TxPipelined(ctx, func(pipe redisclient.Pipeliner) error {
		pipe.RPush(ctx, key, values...)
		pipe.LTrim(ctx, key, int64(-cache.limit), -1)
		pipe.Expire(ctx, key, cache.ttl)
		return nil
	})
	return err
}

func (cache *RedisWorkingCache) Replace(ctx context.Context, userID string, sessionID string, messages []WorkingMessage) error {
	if cache == nil || cache.client == nil {
		return errors.New("working memory redis client is unavailable")
	}
	if len(messages) > cache.limit {
		messages = messages[len(messages)-cache.limit:]
	}
	values := make([]interface{}, 0, len(messages))
	for _, message := range messages {
		if err := message.Validate(); err != nil {
			return err
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			return err
		}
		values = append(values, encoded)
	}
	key := cache.key(userID, sessionID)
	_, err := cache.client.TxPipelined(ctx, func(pipe redisclient.Pipeliner) error {
		pipe.Del(ctx, key)
		if len(values) > 0 {
			pipe.RPush(ctx, key, values...)
			pipe.LTrim(ctx, key, int64(-cache.limit), -1)
			pipe.Expire(ctx, key, cache.ttl)
		}
		return nil
	})
	return err
}

func (cache *RedisWorkingCache) Delete(ctx context.Context, userID string, sessionID string) error {
	if cache == nil || cache.client == nil {
		return errors.New("working memory redis client is unavailable")
	}
	return cache.client.Del(ctx, cache.key(userID, sessionID)).Err()
}

func (*RedisWorkingCache) key(userID string, sessionID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(userID) + "\x00" + strings.TrimSpace(sessionID)))
	return "gopherai:working-memory:v1:" + hex.EncodeToString(digest[:])
}

func boundedWindowLimit(value int) int {
	if value <= 0 || value > DefaultWindowLimit {
		return DefaultWindowLimit
	}
	return value
}
