package model

import "time"

// G10ResumeFactConfirmation is an append-only user attestation over a stable
// set of evidence-backed resume facts. Raw principals and idempotency keys are
// hashed before persistence; selected statement IDs are public report metadata.
type G10ResumeFactConfirmation struct {
	ID                    string    `gorm:"primaryKey;type:char(64)" json:"id"`
	SchemaVersion         string    `gorm:"not null;type:varchar(64)" json:"schema_version"`
	FactSetSHA256         string    `gorm:"index;not null;type:char(64)" json:"fact_set_sha256"`
	SourceReleaseID       string    `gorm:"index;not null;type:varchar(96)" json:"source_release_id"`
	SourceGitSHA          string    `gorm:"not null;type:char(40)" json:"source_git_sha"`
	EvidencePackageSHA256 string    `gorm:"not null;type:char(64)" json:"evidence_package_sha256"`
	SelectedFactIDsJSON   string    `gorm:"type:text;not null" json:"-"`
	SelectedCount         int       `gorm:"not null" json:"selected_count"`
	ReviewerHash          string    `gorm:"index;not null;type:char(64)" json:"-"`
	IdempotencyKeyHash    string    `gorm:"uniqueIndex;not null;type:char(64)" json:"-"`
	RequestSHA256         string    `gorm:"not null;type:char(64)" json:"request_sha256"`
	ConfirmationSHA256    string    `gorm:"uniqueIndex;not null;type:char(64)" json:"confirmation_sha256"`
	CreatedAt             time.Time `gorm:"index;not null" json:"created_at"`
}
