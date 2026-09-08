package model

import "time"

// JudgeCalibrationReview is an append-only human rating. Reviewer identity is
// stored only as a one-way hash and each correction creates a new revision.
type JudgeCalibrationReview struct {
	ID             string    `gorm:"primaryKey;type:char(36)" json:"id"`
	SchemaVersion  string    `gorm:"not null;type:varchar(64)" json:"schema_version"`
	DatasetVersion string    `gorm:"index;not null;type:varchar(64)" json:"dataset_version"`
	DatasetSHA256  string    `gorm:"uniqueIndex:uk_judge_review_revision;index;not null;type:char(64)" json:"dataset_sha256"`
	CaseID         string    `gorm:"uniqueIndex:uk_judge_review_revision;index;not null;type:varchar(64)" json:"case_id"`
	CaseSHA256     string    `gorm:"not null;type:char(64)" json:"case_sha256"`
	ReviewerHash   string    `gorm:"uniqueIndex:uk_judge_review_revision;index;not null;type:char(64)" json:"reviewer_hash"`
	Revision       int       `gorm:"uniqueIndex:uk_judge_review_revision;not null" json:"revision"`
	Relevance      float64   `gorm:"not null" json:"relevance"`
	Completeness   float64   `gorm:"not null" json:"completeness"`
	Helpfulness    float64   `gorm:"not null" json:"helpfulness"`
	Groundedness   float64   `gorm:"not null" json:"groundedness"`
	Safety         float64   `gorm:"not null" json:"safety"`
	Overall        float64   `gorm:"not null" json:"overall"`
	Comment        string    `gorm:"type:text;not null" json:"comment"`
	ReviewSHA256   string    `gorm:"index;not null;type:char(64)" json:"review_sha256"`
	CreatedAt      time.Time `gorm:"index;not null" json:"created_at"`
}
