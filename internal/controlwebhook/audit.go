package controlwebhook

import (
	"context"
	"errors"
	"time"

	"GopherAI/model"

	"gorm.io/gorm"
)

type AuditRepository interface {
	Audit(context.Context, bool, string, int) (AuditSnapshot, error)
}

func (repository *GormRepository) Audit(ctx context.Context, enabled bool, mode string, limit int) (AuditSnapshot, error) {
	snapshot := AuditSnapshot{
		SchemaVersion: SchemaVersion, Enabled: enabled, EndpointMode: mode, Signature: "HMAC-SHA256 v1", MaxAttempts: 3,
		Latest:      []DeliverySummary{},
		Guardrails:  []string{"投递与聊天请求解耦", "签名密钥只从服务器文件读取", "重定向禁用", "最多 3 次尝试", "Dead Letter 可审计但不会自动重放", "Payload 不含用户或请求级标识"},
		Limitations: []string{"当前部署使用 staging loopback receiver 验证签名与幂等；生产环境应替换为 HTTPS 告警平台。"},
	}
	if repository == nil || repository.db == nil || limit < 1 || limit > 50 {
		return snapshot, gorm.ErrInvalidDB
	}
	type statusCount struct {
		Status string
		Count  int64
	}
	var counts []statusCount
	if err := repository.db.WithContext(ctx).Model(&model.ControlWebhookDelivery{}).Select("status, count(*) AS count").Group("status").Scan(&counts).Error; err != nil {
		return snapshot, err
	}
	for _, item := range counts {
		switch item.Status {
		case StatusPending:
			snapshot.Pending = item.Count
		case StatusProcessing:
			snapshot.Processing = item.Count
		case StatusRetry:
			snapshot.Retrying = item.Count
		case StatusDelivered:
			snapshot.Delivered = item.Count
		case StatusDead:
			snapshot.DeadLettered = item.Count
		}
	}
	var rows []model.ControlWebhookDelivery
	if err := repository.db.WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return snapshot, err
	}
	if len(rows) == 0 {
		return snapshot, nil
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.EventID)
	}
	var receipts []model.ControlWebhookReceipt
	if err := repository.db.WithContext(ctx).Where("event_id IN ?", ids).Find(&receipts).Error; err != nil {
		return snapshot, err
	}
	received := make(map[string]model.ControlWebhookReceipt, len(receipts))
	for _, receipt := range receipts {
		received[receipt.EventID] = receipt
	}
	for _, row := range rows {
		receipt, exists := received[row.EventID]
		verified := exists && receipt.PayloadSHA256 == row.PayloadSHA256 && receipt.SignatureVersion == row.SignatureVersion
		snapshot.Latest = append(snapshot.Latest, DeliverySummary{
			EventID: row.EventID, IncidentKey: row.IncidentKey, EventType: row.EventType, Status: row.Status,
			Simulation: row.Simulation,
			Attempt:    row.Attempt, HTTPStatus: row.HTTPStatus, LastErrorCode: row.LastErrorCode,
			PayloadSHA256: row.PayloadSHA256, SignatureVersion: row.SignatureVersion,
			CreatedAt: row.CreatedAt.UTC(), DeliveredAt: utcPointer(row.DeliveredAt), DeadLetteredAt: utcPointer(row.DeadLetteredAt),
			ReceiptVerified: verified,
		})
	}
	return snapshot, nil
}

func utcPointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}

func LatestAudit(ctx context.Context, repository AuditRepository, config Config) (AuditSnapshot, error) {
	if repository == nil {
		return AuditSnapshot{}, errors.New("webhook audit repository is required")
	}
	return repository.Audit(ctx, config.Enabled, config.EndpointMode, 10)
}
