package model

import "time"

// ResolutionFeedback is the immutable, explicit human signal that a diagnostic
// hypothesis was verified and the reported resolution worked. Principal values
// are hashed before persistence and raw diagnostic input is never stored here.
type ResolutionFeedback struct {
	ID               string    `gorm:"primaryKey;type:char(36)" json:"id"`
	TenantIDHash     string    `gorm:"index;not null;type:char(64)" json:"-"`
	UserIDHash       string    `gorm:"uniqueIndex:ux_resolution_feedback_request,priority:1;index;not null;type:char(64)" json:"-"`
	ClientRequestID  string    `gorm:"uniqueIndex:ux_resolution_feedback_request,priority:2;not null;type:varchar(128)" json:"client_request_id"`
	SourceRunID      string    `gorm:"index;not null;type:char(36)" json:"source_run_id"`
	HypothesisID     string    `gorm:"not null;type:varchar(64)" json:"hypothesis_id"`
	ResolutionSHA256 string    `gorm:"not null;type:char(64)" json:"-"`
	FeedbackType     string    `gorm:"not null;type:varchar(32)" json:"feedback_type"`
	CreatedAt        time.Time `json:"created_at"`
}

// ResolvedIncident is authoritative episodic memory. Only explicitly confirmed
// records are eligible for indexing and recall.
type ResolvedIncident struct {
	ID                  string     `gorm:"primaryKey;type:char(36)" json:"id"`
	TenantIDHash        string     `gorm:"index;not null;type:char(64)" json:"-"`
	UserIDHash          string     `gorm:"index;not null;type:char(64)" json:"-"`
	SourceRunID         string     `gorm:"uniqueIndex;not null;type:char(36)" json:"source_run_id"`
	FeedbackID          string     `gorm:"uniqueIndex;not null;type:char(36)" json:"feedback_id"`
	SessionID           string     `gorm:"index;type:char(36)" json:"session_id,omitempty"`
	SchemaVersion       string     `gorm:"not null;type:varchar(32)" json:"schema_version"`
	ExtractorVersion    string     `gorm:"not null;type:varchar(64)" json:"extractor_version"`
	HypothesisID        string     `gorm:"not null;type:varchar(64)" json:"hypothesis_id"`
	Symptom             string     `gorm:"type:text;not null" json:"symptom"`
	RootCause           string     `gorm:"type:text;not null" json:"root_cause"`
	Resolution          string     `gorm:"type:text;not null" json:"resolution"`
	ComponentsJSON      string     `gorm:"type:text;not null" json:"-"`
	ErrorSignaturesJSON string     `gorm:"type:text;not null" json:"-"`
	EvidenceJSON        string     `gorm:"type:text;not null" json:"-"`
	Status              string     `gorm:"index;not null;type:varchar(24)" json:"status"`
	IndexStatus         string     `gorm:"index;not null;type:varchar(24)" json:"index_status"`
	IndexErrorCode      string     `gorm:"type:varchar(64)" json:"index_error_code,omitempty"`
	Version             int        `gorm:"not null" json:"version"`
	ConfirmedAt         time.Time  `gorm:"index;not null" json:"confirmed_at"`
	IndexedAt           *time.Time `json:"indexed_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}
