package model

import "time"

// FailureMiningRun is an immutable snapshot over a bounded online-evaluation
// window. InputHash prevents unchanged windows from creating duplicate runs.
type FailureMiningRun struct {
	ID            string    `gorm:"primaryKey;type:char(64)" json:"id"`
	SchemaVersion string    `gorm:"not null;type:varchar(64)" json:"schema_version"`
	MinerVersion  string    `gorm:"index;not null;type:varchar(64)" json:"miner_version"`
	InputHash     string    `gorm:"uniqueIndex;not null;type:char(64)" json:"input_hash"`
	WindowStart   time.Time `gorm:"index;not null" json:"window_start"`
	WindowEnd     time.Time `gorm:"not null" json:"window_end"`
	SampleCount   int       `gorm:"not null" json:"sample_count"`
	EligibleCount int       `gorm:"not null" json:"eligible_count"`
	ClusterCount  int       `gorm:"not null" json:"cluster_count"`
	ProposalCount int       `gorm:"not null" json:"proposal_count"`
	Status        string    `gorm:"index;not null;type:varchar(32)" json:"status"`
	CreatedAt     time.Time `gorm:"index;not null" json:"created_at"`
}

// FailureCluster deliberately stores only bounded routing and failure
// dimensions. Raw questions, answers, evidence, identities and sample IDs are
// excluded.
type FailureCluster struct {
	ID            string    `gorm:"primaryKey;type:char(64)" json:"id"`
	RunID         string    `gorm:"index;not null;type:char(64)" json:"run_id"`
	SchemaVersion string    `gorm:"not null;type:varchar(64)" json:"schema_version"`
	WhereCode     string    `gorm:"index;not null;type:varchar(64)" json:"where_code"`
	WhyCode       string    `gorm:"index;not null;type:varchar(64)" json:"why_code"`
	PrimaryReason string    `gorm:"index;not null;type:varchar(64)" json:"primary_reason"`
	Intent        string    `gorm:"index;not null;type:varchar(64)" json:"intent"`
	Strategy      string    `gorm:"index;not null;type:varchar(64)" json:"strategy"`
	SampleCount   int       `gorm:"not null" json:"sample_count"`
	SampleSetHash string    `gorm:"not null;type:char(64)" json:"sample_set_hash"`
	Status        string    `gorm:"index;not null;type:varchar(32)" json:"status"`
	CreatedAt     time.Time `gorm:"index;not null" json:"created_at"`
}

// FailureImprovementProposal is an immutable, non-executable candidate. It
// cannot represent source-code, permission, migration, secret or deployment
// changes and never owns an active pointer.
type FailureImprovementProposal struct {
	ID                    string    `gorm:"primaryKey;type:char(64)" json:"id"`
	RunID                 string    `gorm:"index;not null;type:char(64)" json:"run_id"`
	ClusterID             string    `gorm:"index;not null;type:char(64)" json:"cluster_id"`
	SchemaVersion         string    `gorm:"not null;type:varchar(64)" json:"schema_version"`
	CandidateKind         string    `gorm:"index;not null;type:varchar(32)" json:"candidate_kind"`
	Target                string    `gorm:"not null;type:varchar(96)" json:"target"`
	ChangeJSON            string    `gorm:"type:text;not null" json:"-"`
	RationaleCode         string    `gorm:"not null;type:varchar(64)" json:"rationale_code"`
	State                 string    `gorm:"index;not null;type:varchar(32)" json:"state"`
	RequiresHumanReview   bool      `gorm:"not null;default:true" json:"requires_human_review"`
	OfflineGatePassed     bool      `gorm:"not null;default:false" json:"offline_gate_passed"`
	IsolationCanaryPassed bool      `gorm:"not null;default:false" json:"isolation_canary_passed"`
	Applied               bool      `gorm:"not null;default:false" json:"applied"`
	ProposalHash          string    `gorm:"uniqueIndex;not null;type:char(64)" json:"proposal_hash"`
	CreatedAt             time.Time `gorm:"index;not null" json:"created_at"`
}
