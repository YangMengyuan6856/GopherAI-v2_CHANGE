package toolruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"GopherAI/model"

	"gorm.io/gorm"
)

type GormAuditor struct{ database *gorm.DB }

func NewGormAuditor(database *gorm.DB) *GormAuditor { return &GormAuditor{database: database} }

func (auditor *GormAuditor) Record(ctx context.Context, invocation Invocation, message ToolMessage) error {
	if auditor == nil || auditor.database == nil {
		return errors.New("tool audit database is unavailable")
	}
	record := model.ToolAudit{
		CallID: message.CallID, TenantIDHash: hashPrincipal(invocation.Principal.TenantID), UserIDHash: hashPrincipal(invocation.Principal.UserID),
		ToolName: boundedAuditValue(message.ToolName, 64), ToolVersion: boundedAuditValue(message.ToolVersion, 32), ArgsHash: message.ArgsHash,
		Intent: boundedAuditValue(invocation.Intent, 32), Strategy: boundedAuditValue(invocation.Strategy, 64), Status: boundedAuditValue(message.Status, 32),
		ErrorCode: boundedAuditValue(message.ErrorCode, 64), Retryable: message.Retryable, LatencyMS: message.LatencyMS,
		Cached: message.Cached, Truncated: message.Truncated,
	}
	return auditor.database.WithContext(ctx).Create(&record).Error
}

func hashPrincipal(value string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(digest[:])
}

func boundedAuditValue(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) > maximum {
		return value[:maximum]
	}
	return value
}
