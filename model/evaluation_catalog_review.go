package model

import "time"

// EvaluationCatalogReview is one append-only human decision over an immutable
// catalog case. Principal and idempotency values are stored only as hashes.
type EvaluationCatalogReview struct {
	ID                   string    `gorm:"primaryKey;type:char(64)" json:"id"`
	SchemaVersion        string    `gorm:"not null;type:varchar(64)" json:"schema_version"`
	DatasetVersion       string    `gorm:"index;not null;type:varchar(64)" json:"dataset_version"`
	CatalogSHA256        string    `gorm:"uniqueIndex:uk_catalog_governance_review_revision;index;not null;type:char(64)" json:"catalog_sha256"`
	GovernanceSHA256     string    `gorm:"uniqueIndex:uk_catalog_governance_review_revision;index;not null;type:char(64)" json:"governance_sha256"`
	Slice                string    `gorm:"index;not null;type:varchar(64)" json:"slice"`
	CaseID               string    `gorm:"uniqueIndex:uk_catalog_governance_review_revision;index;not null;type:varchar(96)" json:"case_id"`
	CaseSHA256           string    `gorm:"not null;type:char(64)" json:"case_sha256"`
	ReviewerHash         string    `gorm:"uniqueIndex:uk_catalog_governance_review_revision;index;not null;type:char(64)" json:"-"`
	Revision             int       `gorm:"uniqueIndex:uk_catalog_governance_review_revision;not null" json:"revision"`
	Decision             string    `gorm:"index;not null;type:varchar(24)" json:"decision"`
	ReasonCodesJSON      string    `gorm:"type:text;not null" json:"-"`
	Comment              string    `gorm:"type:text;not null" json:"comment"`
	ExpectedRevision     int       `gorm:"not null" json:"expected_revision"`
	IdempotencyKeyHash   string    `gorm:"uniqueIndex;not null;type:char(64)" json:"-"`
	RequestSHA256        string    `gorm:"not null;type:char(64)" json:"request_sha256"`
	ReviewSHA256         string    `gorm:"uniqueIndex;not null;type:char(64)" json:"review_sha256"`
	PreviousReviewSHA256 string    `gorm:"type:char(64)" json:"-"`
	CreatedAt            time.Time `gorm:"index;not null" json:"created_at"`
}
