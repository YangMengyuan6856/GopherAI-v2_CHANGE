package model

import "time"

// MetricWindowSnapshot is a durable, low-cardinality observation captured from
// an allowlisted Prometheus recording rule. Raw PromQL, request identifiers and
// user/tenant dimensions are deliberately not stored here.
type MetricWindowSnapshot struct {
	ID            uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	SnapshotKey   string    `gorm:"uniqueIndex;not null;type:char(64)" json:"snapshot_key"`
	BatchID       string    `gorm:"index;not null;type:char(64)" json:"batch_id"`
	Metric        string    `gorm:"index:idx_metric_scope_time,priority:1;not null;type:varchar(64)" json:"metric"`
	Strategy      string    `gorm:"index:idx_metric_scope_time,priority:2;not null;type:varchar(64)" json:"strategy"`
	DataStatus    string    `gorm:"index;not null;type:varchar(24)" json:"data_status"`
	Value         float64   `gorm:"type:double;not null" json:"value"`
	Population    int64     `gorm:"not null" json:"population"`
	WindowSeconds int       `gorm:"not null" json:"window_seconds"`
	ObservedAt    time.Time `gorm:"index:idx_metric_scope_time,priority:3;not null" json:"observed_at"`
	CollectedAt   time.Time `gorm:"index;not null" json:"collected_at"`
	RulesVersion  string    `gorm:"not null;type:varchar(64)" json:"rules_version"`
	RulesSHA256   string    `gorm:"not null;type:char(64)" json:"rules_sha256"`
	Collector     string    `gorm:"not null;type:varchar(64)" json:"collector"`
	CreatedAt     time.Time `json:"created_at"`
}
