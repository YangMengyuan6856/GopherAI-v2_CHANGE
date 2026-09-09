package model

import "time"

// FaultInjectionCampaign stores one immutable, isolated acceptance report. It
// never represents a production fault and can never activate a policy.
type FaultInjectionCampaign struct {
	CampaignID     string    `gorm:"primaryKey;type:varchar(40)" json:"campaign_id"`
	SchemaVersion  string    `gorm:"not null;type:varchar(64)" json:"schema_version"`
	FixtureVersion string    `gorm:"index;not null;type:varchar(64)" json:"fixture_version"`
	Environment    string    `gorm:"index;not null;type:varchar(64)" json:"environment"`
	Mode           string    `gorm:"index;not null;type:varchar(24)" json:"mode"`
	ReportJSON     string    `gorm:"type:longtext;not null" json:"-"`
	ReportSHA256   string    `gorm:"not null;type:char(64)" json:"report_sha256"`
	Simulation     bool      `gorm:"index;not null;default:true" json:"simulation"`
	Applied        bool      `gorm:"not null;default:false" json:"applied"`
	CreatedAt      time.Time `gorm:"index;not null" json:"created_at"`
}
