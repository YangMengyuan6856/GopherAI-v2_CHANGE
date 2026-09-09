package model

import "time"

// EnvironmentMemory stores one versioned profile fact. Principal values are
// hashed; inferred observations remain candidates until the user confirms or
// corrects them.
type EnvironmentMemory struct {
	ID             string     `gorm:"primaryKey;type:char(36)" json:"id"`
	TenantIDHash   string     `gorm:"index;not null;type:char(64)" json:"-"`
	UserIDHash     string     `gorm:"index;uniqueIndex:ux_environment_source,priority:1;not null;type:char(64)" json:"-"`
	Key            string     `gorm:"index;not null;type:varchar(64)" json:"key"`
	Value          string     `gorm:"type:varchar(256);not null" json:"value"`
	SourceType     string     `gorm:"index;not null;type:varchar(32)" json:"source_type"`
	SourceRunID    string     `gorm:"index;type:char(36)" json:"source_run_id,omitempty"`
	SourceRef      string     `gorm:"uniqueIndex:ux_environment_source,priority:2;type:char(36);not null" json:"-"`
	SourceKey      string     `gorm:"uniqueIndex:ux_environment_source,priority:3;type:varchar(64);not null" json:"-"`
	ParentID       string     `gorm:"index;type:char(36)" json:"parent_id,omitempty"`
	Confidence     float64    `gorm:"not null" json:"confidence"`
	Status         string     `gorm:"index;not null;type:varchar(24)" json:"status"`
	Version        int        `gorm:"not null" json:"version"`
	ExpiresAt      *time.Time `gorm:"index" json:"expires_at,omitempty"`
	LastObservedAt time.Time  `gorm:"index;not null" json:"last_observed_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
