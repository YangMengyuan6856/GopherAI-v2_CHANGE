package controlwebhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"GopherAI/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type DeliveryRepository interface {
	ClaimAvailable(context.Context, time.Time, time.Duration) (*model.ControlWebhookDelivery, error)
	MarkDelivered(context.Context, string, time.Time, int) error
	MarkRetry(context.Context, string, time.Time, int, string) error
	MarkDead(context.Context, string, time.Time, int, string) error
}

type DeliveryObserver interface {
	RecordWebhookDelivery(eventType string, status string)
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Dispatcher struct {
	config     Config
	repository DeliveryRepository
	client     HTTPDoer
	observer   DeliveryObserver
	clock      func() time.Time
	logger     *log.Logger
}

func NewDispatcher(config Config, repository DeliveryRepository, client HTTPDoer, observer DeliveryObserver, logger *log.Logger) (*Dispatcher, error) {
	if !config.Enabled || config.Endpoint == nil || len(config.Secret) < 32 || repository == nil || client == nil ||
		config.RequestTimeout <= 0 || config.PollInterval <= 0 || config.LeaseDuration <= 0 || config.MaxAttempts < 1 || config.MaxAttempts > 5 {
		return nil, errors.New("control webhook dispatcher is not configured")
	}
	return &Dispatcher{config: config, repository: repository, client: client, observer: observer, clock: time.Now, logger: logger}, nil
}

func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       4 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("webhook redirects are disabled") },
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second,
			TLSHandshakeTimeout: 2 * time.Second, ResponseHeaderTimeout: 3 * time.Second, ExpectContinueTimeout: time.Second,
		},
	}
}

func (dispatcher *Dispatcher) Run(ctx context.Context) error {
	if dispatcher == nil {
		return errors.New("control webhook dispatcher is nil")
	}
	ticker := time.NewTicker(dispatcher.config.PollInterval)
	defer ticker.Stop()
	for {
		if _, err := dispatcher.DispatchAvailable(ctx, 20); err != nil && ctx.Err() == nil && dispatcher.logger != nil {
			dispatcher.logger.Printf(`{"event":"control_webhook_dispatch","status":"error","reason_code":"%s"}`, stableDeliveryError(err))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (dispatcher *Dispatcher) DispatchAvailable(ctx context.Context, limit int) (int, error) {
	if dispatcher == nil || dispatcher.repository == nil || dispatcher.clock == nil || limit < 1 || limit > 100 {
		return 0, errors.New("control webhook dispatch request is invalid")
	}
	processed := 0
	var joined error
	for processed < limit {
		now := dispatcher.clock().UTC()
		delivery, err := dispatcher.repository.ClaimAvailable(ctx, now, dispatcher.config.LeaseDuration)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			break
		}
		if err != nil {
			return processed, errors.Join(joined, err)
		}
		processed++
		status, retryable, code := dispatcher.deliver(ctx, *delivery, now)
		if code == "" {
			if err := dispatcher.repository.MarkDelivered(ctx, delivery.EventID, dispatcher.clock().UTC(), status); err != nil {
				joined = errors.Join(joined, err)
				dispatcher.record(*delivery, "error")
			} else {
				dispatcher.record(*delivery, "success")
			}
			continue
		}
		if retryable && delivery.Attempt < dispatcher.config.MaxAttempts {
			next := now.Add(deliveryBackoff(delivery.Attempt))
			if err := dispatcher.repository.MarkRetry(ctx, delivery.EventID, next, status, code); err != nil {
				joined = errors.Join(joined, err)
			}
			dispatcher.record(*delivery, "retry")
			continue
		}
		if err := dispatcher.repository.MarkDead(ctx, delivery.EventID, now, status, code); err != nil {
			joined = errors.Join(joined, err)
		}
		dispatcher.record(*delivery, "dead")
	}
	return processed, joined
}

func (dispatcher *Dispatcher) deliver(parent context.Context, delivery model.ControlWebhookDelivery, now time.Time) (int, bool, string) {
	if len(delivery.EventID) != 64 || !validEventType(delivery.EventType) || !json.Valid([]byte(delivery.PayloadJSON)) || stablePayloadSHA(delivery.PayloadJSON) != delivery.PayloadSHA256 {
		return 0, false, ErrorInvalidPayload
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := Sign(dispatcher.config.Secret, timestamp, []byte(delivery.PayloadJSON))
	ctx, cancel := context.WithTimeout(parent, dispatcher.config.RequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, dispatcher.config.Endpoint.String(), bytes.NewBufferString(delivery.PayloadJSON))
	if err != nil {
		return 0, false, ErrorInvalidPayload
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "GopherAI-Control-Webhook/1.0")
	request.Header.Set("X-GopherAI-Event-ID", delivery.EventID)
	request.Header.Set("X-GopherAI-Event-Type", delivery.EventType)
	request.Header.Set("X-GopherAI-Timestamp", timestamp)
	request.Header.Set("X-GopherAI-Signature", SignatureVersion+"="+signature)
	response, err := dispatcher.client.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return 0, true, ErrorTimeout
		}
		return 0, true, ErrorTransport
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response.StatusCode, false, ""
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return response.StatusCode, true, ErrorRateLimited
	}
	if response.StatusCode >= 500 {
		return response.StatusCode, true, ErrorRemoteServer
	}
	return response.StatusCode, false, ErrorRemoteRejected
}

func (dispatcher *Dispatcher) record(delivery model.ControlWebhookDelivery, status string) {
	if dispatcher.observer != nil && !delivery.Simulation {
		dispatcher.observer.RecordWebhookDelivery(delivery.EventType, status)
	}
}

func Sign(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func deliveryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 5 {
		attempt = 5
	}
	return time.Duration(1<<uint(attempt-1)) * time.Second
}

func stablePayloadSHA(payload string) string {
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:])
}

func stableDeliveryError(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "WEBHOOK_CONTEXT_DONE"
	}
	return "WEBHOOK_DELIVERY_CYCLE_FAILED"
}

func (repository *GormRepository) ClaimAvailable(ctx context.Context, now time.Time, lease time.Duration) (*model.ControlWebhookDelivery, error) {
	if repository == nil || repository.db == nil || lease <= 0 {
		return nil, gorm.ErrInvalidDB
	}
	var claimed model.ControlWebhookDelivery
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.ControlWebhookDelivery
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("available_at <= ? AND (status IN ? OR (status = ? AND lease_until <= ?))", now, []string{StatusPending, StatusRetry}, StatusProcessing, now).
			Order("available_at ASC, created_at ASC").Limit(1).Find(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		leaseUntil := now.Add(lease)
		result = tx.Model(&model.ControlWebhookDelivery{}).Where("event_id = ?", row.EventID).Updates(map[string]any{
			"status": StatusProcessing, "attempt": row.Attempt + 1, "lease_until": leaseUntil, "updated_at": now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("control webhook claim was lost")
		}
		row.Status, row.Attempt, row.LeaseUntil = StatusProcessing, row.Attempt+1, &leaseUntil
		claimed = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &claimed, nil
}

func (repository *GormRepository) MarkDelivered(ctx context.Context, eventID string, at time.Time, httpStatus int) error {
	return repository.finish(ctx, eventID, map[string]any{"status": StatusDelivered, "delivered_at": at, "lease_until": nil, "http_status": httpStatus, "last_error_code": "", "updated_at": at})
}

func (repository *GormRepository) MarkRetry(ctx context.Context, eventID string, availableAt time.Time, httpStatus int, code string) error {
	return repository.finish(ctx, eventID, map[string]any{"status": StatusRetry, "available_at": availableAt, "lease_until": nil, "http_status": httpStatus, "last_error_code": code, "updated_at": time.Now().UTC()})
}

func (repository *GormRepository) MarkDead(ctx context.Context, eventID string, at time.Time, httpStatus int, code string) error {
	return repository.finish(ctx, eventID, map[string]any{"status": StatusDead, "dead_lettered_at": at, "lease_until": nil, "http_status": httpStatus, "last_error_code": code, "updated_at": at})
}

func (repository *GormRepository) finish(ctx context.Context, eventID string, updates map[string]any) error {
	if repository == nil || repository.db == nil || len(eventID) != 64 {
		return gorm.ErrInvalidDB
	}
	result := repository.db.WithContext(ctx).Model(&model.ControlWebhookDelivery{}).Where("event_id = ? AND status = ?", eventID, StatusProcessing).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("control webhook completion lost for %s", eventID[:8])
	}
	return nil
}

func validSignatureHeader(value string) (string, bool) {
	prefix := SignatureVersion + "="
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return "", false
	}
	value = strings.TrimPrefix(value, prefix)
	_, err := hex.DecodeString(value)
	return value, err == nil
}
