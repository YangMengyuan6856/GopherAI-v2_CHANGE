package model

import "time"

// UserFeedbackEvent is the immutable, privacy-bounded audit record for an
// explicit answer rating. The rated content lives only in the separately
// retained online-evaluation sample; raw user and request identifiers are not
// duplicated here.
type UserFeedbackEvent struct {
	ID            string    `gorm:"primaryKey;type:char(36)" json:"id"`
	UserHash      string    `gorm:"uniqueIndex:ux_user_feedback_request,priority:1;index;not null;type:char(64)" json:"-"`
	RequestHash   string    `gorm:"uniqueIndex:ux_user_feedback_request,priority:2;index;not null;type:char(64)" json:"-"`
	FeedbackType  string    `gorm:"uniqueIndex:ux_user_feedback_request,priority:3;index;not null;type:varchar(32)" json:"feedback_type"`
	TenantHash    string    `gorm:"index;not null;type:char(64)" json:"-"`
	TraceHash     string    `gorm:"index;not null;type:char(64)" json:"-"`
	SampleID      string    `gorm:"uniqueIndex;not null;type:char(36)" json:"sample_id"`
	Strategy      string    `gorm:"index;not null;type:varchar(64)" json:"strategy"`
	PolicyVersion string    `gorm:"index;not null;type:varchar(64)" json:"policy_version"`
	PayloadHash   string    `gorm:"not null;type:char(64)" json:"payload_hash"`
	CreatedAt     time.Time `gorm:"index;not null" json:"created_at"`
}
