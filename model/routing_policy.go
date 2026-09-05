package model

import "time"

// RoutingPolicy stores immutable policy content. ActiveSlot is populated only
// for the active row so MySQL, rather than process memory or Redis, enforces one
// active policy per environment.
type RoutingPolicy struct {
	ID                   uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Version              string     `gorm:"uniqueIndex;not null;type:varchar(64)" json:"version"`
	Environment          string     `gorm:"index;not null;type:varchar(32)" json:"environment"`
	Status               string     `gorm:"index;not null;type:varchar(24)" json:"status"`
	PolicyJSON           string     `gorm:"type:longtext;not null" json:"-"`
	PolicyHash           string     `gorm:"not null;type:char(64)" json:"policy_hash"`
	ParentVersion        string     `gorm:"type:varchar(64)" json:"parent_version,omitempty"`
	Reason               string     `gorm:"type:varchar(255)" json:"reason"`
	EvidenceSnapshotJSON string     `gorm:"type:longtext" json:"-"`
	CreatedBy            string     `gorm:"not null;type:varchar(32)" json:"created_by"`
	ActiveSlot           *string    `gorm:"uniqueIndex;type:varchar(64)" json:"-"`
	ActivatedAt          *time.Time `json:"activated_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}
