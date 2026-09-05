package model

import "time"

// ControlRecommendation is an append-only control-plane decision. The
// application exposes no update or activation operation for this record: a
// recommendation can describe a candidate policy, but cannot become active.
type ControlRecommendation struct {
	RecommendationID       string    `gorm:"primaryKey;type:char(64)" json:"recommendation_id"`
	Mode                   string    `gorm:"index;not null;type:varchar(24)" json:"mode"`
	Status                 string    `gorm:"index;not null;type:varchar(24)" json:"status"`
	Simulation             bool      `gorm:"index;not null;default:false" json:"simulation"`
	Source                 string    `gorm:"not null;type:varchar(48)" json:"source"`
	IncidentKey            string    `gorm:"index;not null;type:varchar(64)" json:"incident_key"`
	BatchID                string    `gorm:"index;not null;type:char(64)" json:"batch_id"`
	Metric                 string    `gorm:"index;not null;type:varchar(64)" json:"metric"`
	Strategy               string    `gorm:"index;not null;type:varchar(64)" json:"strategy"`
	Intent                 string    `gorm:"not null;type:varchar(64)" json:"intent,omitempty"`
	ParentPolicyVersion    string    `gorm:"not null;type:varchar(64)" json:"parent_policy_version"`
	ParentPolicySHA256     string    `gorm:"not null;type:char(64)" json:"parent_policy_sha256"`
	CandidatePolicyVersion string    `gorm:"type:varchar(64)" json:"candidate_policy_version,omitempty"`
	CandidatePolicySHA256  string    `gorm:"type:char(64)" json:"candidate_policy_sha256,omitempty"`
	CandidatePolicyJSON    string    `gorm:"type:longtext" json:"-"`
	BeforeWeightBasis      int       `gorm:"not null;default:0" json:"before_weight_basis"`
	ProposedWeightBasis    int       `gorm:"not null;default:0" json:"proposed_weight_basis"`
	FallbackStrategy       string    `gorm:"type:varchar(64)" json:"fallback_strategy,omitempty"`
	FallbackBeforeBasis    int       `gorm:"not null;default:0" json:"fallback_before_basis"`
	FallbackProposedBasis  int       `gorm:"not null;default:0" json:"fallback_proposed_basis"`
	WeightDeltaBasis       int       `gorm:"not null;default:0" json:"weight_delta_basis"`
	ReasonCode             string    `gorm:"index;not null;type:varchar(64)" json:"reason_code"`
	EvidenceJSON           string    `gorm:"type:longtext;not null" json:"-"`
	EvidenceSHA256         string    `gorm:"not null;type:char(64)" json:"evidence_sha256"`
	EvaluationRunID        string    `gorm:"type:varchar(64)" json:"evaluation_run_id,omitempty"`
	EvaluationReportSHA256 string    `gorm:"type:char(64)" json:"evaluation_report_sha256,omitempty"`
	BaselineEligible       bool      `gorm:"not null;default:false" json:"baseline_eligible"`
	DetectorVersion        string    `gorm:"not null;type:varchar(64)" json:"detector_version"`
	RulesVersion           string    `gorm:"not null;type:varchar(64)" json:"rules_version"`
	RulesSHA256            string    `gorm:"not null;type:char(64)" json:"rules_sha256"`
	Applied                bool      `gorm:"not null;default:false" json:"applied"`
	CreatedAt              time.Time `gorm:"index;not null" json:"created_at"`
}
