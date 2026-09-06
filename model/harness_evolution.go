package model

import "time"

// HarnessArtifact is an immutable, offline-only candidate. It deliberately has
// no active pointer and cannot represent source code, permission or deployment
// changes.
type HarnessArtifact struct {
	ID                      string    `gorm:"primaryKey;type:char(64)" json:"id"`
	SchemaVersion           string    `gorm:"not null;type:varchar(64)" json:"schema_version"`
	ArtifactType            string    `gorm:"index;not null;type:varchar(48)" json:"artifact_type"`
	ArtifactVersion         string    `gorm:"uniqueIndex;not null;type:varchar(96)" json:"artifact_version"`
	ParentVersion           string    `gorm:"not null;type:varchar(96)" json:"parent_version"`
	ParentSHA256            string    `gorm:"not null;type:char(64)" json:"parent_sha256"`
	PatchJSON               string    `gorm:"type:text;not null" json:"-"`
	PatchSHA256             string    `gorm:"not null;type:char(64)" json:"patch_sha256"`
	TargetClusterID         string    `gorm:"index;not null;type:char(64)" json:"target_cluster_id"`
	SourceProposalID        string    `gorm:"index;not null;type:char(64)" json:"source_proposal_id"`
	SourceProposalSHA256    string    `gorm:"not null;type:char(64)" json:"source_proposal_sha256"`
	ProposerVersion         string    `gorm:"not null;type:varchar(64)" json:"proposer_version"`
	ValidatorVersion        string    `gorm:"not null;type:varchar(64)" json:"validator_version"`
	DataSplit               string    `gorm:"index;not null;type:varchar(32)" json:"data_split"`
	DataVersion             string    `gorm:"not null;type:char(64)" json:"data_version"`
	DataSHA256              string    `gorm:"not null;type:char(64)" json:"data_sha256"`
	BudgetJSON              string    `gorm:"type:varchar(256);not null" json:"-"`
	Status                  string    `gorm:"index;not null;type:varchar(32)" json:"status"`
	StaticValidationPassed  bool      `gorm:"not null;default:false" json:"static_validation_passed"`
	RequiresHumanApproval   bool      `gorm:"not null;default:true" json:"requires_human_approval"`
	OfflineEvaluationPassed bool      `gorm:"not null;default:false" json:"offline_evaluation_passed"`
	HoldoutOpened           bool      `gorm:"not null;default:false" json:"holdout_opened"`
	Applied                 bool      `gorm:"not null;default:false" json:"applied"`
	RollbackVersion         string    `gorm:"not null;type:varchar(96)" json:"rollback_version"`
	ArtifactSHA256          string    `gorm:"uniqueIndex;not null;type:char(64)" json:"artifact_sha256"`
	CreatedAt               time.Time `gorm:"index;not null" json:"created_at"`
}

// HarnessArtifactReview is append-only human governance metadata. Creating a
// review never mutates the artifact and never activates it.
type HarnessArtifactReview struct {
	ID             string    `gorm:"primaryKey;type:char(64)" json:"id"`
	ArtifactID     string    `gorm:"index;not null;type:char(64)" json:"artifact_id"`
	Decision       string    `gorm:"index;not null;type:varchar(24)" json:"decision"`
	ReasonCode     string    `gorm:"not null;type:varchar(64)" json:"reason_code"`
	ReviewerHash   string    `gorm:"index;not null;type:char(64)" json:"-"`
	ParentReviewID string    `gorm:"type:char(64)" json:"parent_review_id,omitempty"`
	ReviewSHA256   string    `gorm:"uniqueIndex;not null;type:char(64)" json:"review_sha256"`
	CreatedAt      time.Time `gorm:"index;not null" json:"created_at"`
}
