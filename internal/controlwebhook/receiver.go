package controlwebhook

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"GopherAI/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maximumReceiverClockSkew = 5 * time.Minute

type ReceiptRepository interface {
	StoreReceipt(context.Context, model.ControlWebhookReceipt) (bool, error)
}

type Receiver struct {
	config     Config
	repository ReceiptRepository
	clock      func() time.Time
}

func NewReceiver(config Config, repository ReceiptRepository) *Receiver {
	return &Receiver{config: config, repository: repository, clock: time.Now}
}

func (receiver *Receiver) Receive(ctx context.Context, remoteAddr string, header http.Header, body []byte) (bool, string, error) {
	if receiver == nil || !receiver.config.Enabled || !receiver.config.LoopbackReceiver || receiver.repository == nil || receiver.clock == nil {
		return false, "WEBHOOK_RECEIVER_DISABLED", errors.New("webhook receiver is disabled")
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return false, "WEBHOOK_SOURCE_REJECTED", errors.New("webhook source is not loopback")
	}
	if len(body) == 0 || len(body) > 64<<10 || !json.Valid(body) {
		return false, ErrorInvalidPayload, errors.New("webhook body is invalid")
	}
	timestampText := strings.TrimSpace(header.Get("X-GopherAI-Timestamp"))
	timestampUnix, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		return false, ErrorInvalidSignature, errors.New("webhook timestamp is invalid")
	}
	now := receiver.clock().UTC()
	clockDelta := now.Sub(time.Unix(timestampUnix, 0).UTC())
	if clockDelta < -maximumReceiverClockSkew || clockDelta > maximumReceiverClockSkew {
		return false, ErrorInvalidSignature, errors.New("webhook timestamp is outside replay window")
	}
	signatureText, ok := validSignatureHeader(strings.TrimSpace(header.Get("X-GopherAI-Signature")))
	if !ok {
		return false, ErrorInvalidSignature, errors.New("webhook signature header is invalid")
	}
	expected, _ := hex.DecodeString(Sign(receiver.config.Secret, timestampText, body))
	actual, _ := hex.DecodeString(signatureText)
	if !hmac.Equal(actual, expected) {
		return false, ErrorInvalidSignature, errors.New("webhook signature mismatch")
	}
	var payload Payload
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.SchemaVersion != SchemaVersion || len(payload.EventID) != 64 || !validEventType(payload.EventType) || payload.IncidentKey == "" || payload.Detector.Applied || len(payload.Provenance.BatchID) != 64 || len(payload.Provenance.RulesSHA256) != 64 {
		return false, ErrorInvalidPayload, errors.New("webhook payload contract is invalid")
	}
	if (payload.Simulation && payload.FixtureMode != AcceptanceFixtureMode) || (!payload.Simulation && payload.FixtureMode != "") {
		return false, ErrorInvalidPayload, errors.New("webhook fixture boundary is invalid")
	}
	if header.Get("X-GopherAI-Event-ID") != payload.EventID || header.Get("X-GopherAI-Event-Type") != payload.EventType {
		return false, ErrorInvalidPayload, errors.New("webhook identity headers do not match payload")
	}
	receipt := model.ControlWebhookReceipt{
		EventID: payload.EventID, EventType: payload.EventType, PayloadSHA256: stablePayloadSHA(string(body)),
		Simulation: payload.Simulation, SignatureVersion: SignatureVersion, ReceivedAt: now, CreatedAt: now,
	}
	duplicate, err := receiver.repository.StoreReceipt(ctx, receipt)
	if err != nil {
		return false, "WEBHOOK_RECEIPT_PERSIST_FAILED", err
	}
	return duplicate, "", nil
}

func (repository *GormRepository) StoreReceipt(ctx context.Context, receipt model.ControlWebhookReceipt) (bool, error) {
	if repository == nil || repository.db == nil || len(receipt.EventID) != 64 || len(receipt.PayloadSHA256) != 64 || !validEventType(receipt.EventType) {
		return false, gorm.ErrInvalidDB
	}
	result := repository.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 1 {
		return false, nil
	}
	var existing model.ControlWebhookReceipt
	if err := repository.db.WithContext(ctx).Where("event_id = ?", receipt.EventID).First(&existing).Error; err != nil {
		return false, err
	}
	if existing.EventType != receipt.EventType || existing.Simulation != receipt.Simulation || existing.PayloadSHA256 != receipt.PayloadSHA256 || existing.SignatureVersion != receipt.SignatureVersion {
		return false, errors.New("webhook event id was reused with different content")
	}
	return true, nil
}
