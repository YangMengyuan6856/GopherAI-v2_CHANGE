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

// HarnessPromotionAttempt is an append-only human gate audit. Blocked approval
// attempts are persisted as first-class evidence and never mutate an artifact.
type HarnessPromotionAttempt struct {
	ID                   string    `gorm:"primaryKey;type:char(64)" json:"id"`
	SchemaVersion        string    `gorm:"not null;type:varchar(64)" json:"schema_version"`
	ExperimentVersion    string    `gorm:"index;not null;type:varchar(64)" json:"experiment_version"`
	CandidateSHA256      string    `gorm:"index;not null;type:char(64)" json:"candidate_sha256"`
	ReportSHA256         string    `gorm:"index;not null;type:char(64)" json:"report_sha256"`
	RequestedDecision    string    `gorm:"index;not null;type:varchar(24)" json:"requested_decision"`
	Outcome              string    `gorm:"index;not null;type:varchar(24)" json:"outcome"`
	ReasonCode           string    `gorm:"index;not null;type:varchar(64)" json:"reason_code"`
	BaseVersion          string    `gorm:"not null;type:varchar(96)" json:"base_version"`
	ReviewerHash         string    `gorm:"index;not null;type:char(64)" json:"-"`
	IdempotencyKeyHash   string    `gorm:"uniqueIndex;not null;type:char(64)" json:"-"`
	ActivePointerChanged bool      `gorm:"not null;default:false" json:"active_pointer_changed"`
	AttemptSHA256        string    `gorm:"uniqueIndex;not null;type:char(64)" json:"attempt_sha256"`
	CreatedAt            time.Time `gorm:"index;not null" json:"created_at"`
}

// HarnessActivePointer selects an artifact only inside the isolated Shadow
// runtime. It is intentionally separate from the production routing policy.
type HarnessActivePointer struct {
	ID              string    `gorm:"primaryKey;type:char(64)" json:"id"`
	Scope           string    `gorm:"uniqueIndex:idx_harness_pointer_scope_type;not null;type:varchar(32)" json:"scope"`
	ArtifactType    string    `gorm:"uniqueIndex:idx_harness_pointer_scope_type;not null;type:varchar(48)" json:"artifact_type"`
	CurrentVersion  string    `gorm:"not null;type:varchar(96)" json:"current_version"`
	CurrentSHA256   string    `gorm:"not null;type:char(64)" json:"current_sha256"`
	PreviousVersion string    `gorm:"type:varchar(96)" json:"previous_version,omitempty"`
	PreviousSHA256  string    `gorm:"type:char(64)" json:"previous_sha256,omitempty"`
	StateVersion    uint64    `gorm:"not null" json:"state_version"`
	LastTransition  string    `gorm:"not null;type:varchar(64)" json:"last_transition"`
	LastEventSHA256 string    `gorm:"not null;type:char(64)" json:"last_event_sha256"`
	UpdatedByHash   string    `gorm:"not null;type:char(64)" json:"-"`
	UpdatedAt       time.Time `gorm:"index;not null" json:"updated_at"`
}

// HarnessControlEvent is an append-only audit for blocked and applied Shadow
// activation/rollback requests. Raw principals and idempotency keys are never
// stored.
type HarnessControlEvent struct {
	ID                   string    `gorm:"primaryKey;type:char(64)" json:"id"`
	SchemaVersion        string    `gorm:"not null;type:varchar(64)" json:"schema_version"`
	Operation            string    `gorm:"index;not null;type:varchar(32)" json:"operation"`
	Scope                string    `gorm:"index;not null;type:varchar(32)" json:"scope"`
	ArtifactType         string    `gorm:"index;not null;type:varchar(48)" json:"artifact_type"`
	ExperimentVersion    string    `gorm:"type:varchar(64)" json:"experiment_version,omitempty"`
	CandidateVersion     string    `gorm:"type:varchar(96)" json:"candidate_version,omitempty"`
	CandidateSHA256      string    `gorm:"type:char(64)" json:"candidate_sha256,omitempty"`
	ReportSHA256         string    `gorm:"type:char(64)" json:"report_sha256,omitempty"`
	ExpectedStateVersion uint64    `gorm:"not null" json:"expected_state_version"`
	Outcome              string    `gorm:"index;not null;type:varchar(24)" json:"outcome"`
	ReasonCode           string    `gorm:"index;not null;type:varchar(64)" json:"reason_code"`
	PointerChanged       bool      `gorm:"not null;default:false" json:"pointer_changed"`
	BeforeVersion        string    `gorm:"type:varchar(96)" json:"before_version,omitempty"`
	BeforeSHA256         string    `gorm:"type:char(64)" json:"before_sha256,omitempty"`
	BeforeStateVersion   uint64    `gorm:"not null" json:"before_state_version"`
	AfterVersion         string    `gorm:"type:varchar(96)" json:"after_version,omitempty"`
	AfterSHA256          string    `gorm:"type:char(64)" json:"after_sha256,omitempty"`
	AfterStateVersion    uint64    `gorm:"not null" json:"after_state_version"`
	ShadowReportSHA256   string    `gorm:"type:char(64)" json:"shadow_report_sha256,omitempty"`
	ActorHash            string    `gorm:"index;not null;type:char(64)" json:"-"`
	IdempotencyKeyHash   string    `gorm:"uniqueIndex;not null;type:char(64)" json:"-"`
	RequestSHA256        string    `gorm:"not null;type:char(64)" json:"request_sha256"`
	EventSHA256          string    `gorm:"uniqueIndex;not null;type:char(64)" json:"event_sha256"`
	CreatedAt            time.Time `gorm:"index;not null" json:"created_at"`
}
