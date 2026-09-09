package evolution

import (
	"encoding/json"
	"time"
)

const (
	SchemaVersion    = "harness-evolution-lineage-v1"
	ProposerVersion  = "failure-cluster-minimal-patch-v1"
	ValidatorVersion = "harness-candidate-static-validator-v1"
	Mode             = "offline_candidate_only"
	StatusValidated  = "validated_candidate"
	DataSplit        = "evolution"
)

type MinimalPatch struct {
	Operation     string          `json:"operation"`
	Path          string          `json:"path"`
	Value         json.RawMessage `json:"value"`
	VariableCount int             `json:"variable_count"`
}

// ExecutionBudget is deliberately model-free in the candidate generation
// stage. Fair inference budgets belong to the later A/B gate.
type ExecutionBudget struct {
	ModelCalls       int `json:"model_calls"`
	TokenBudget      int `json:"token_budget"`
	SearchIterations int `json:"search_iterations"`
}

type ArtifactView struct {
	ID                      string          `json:"id"`
	ArtifactType            string          `json:"artifact_type"`
	ArtifactVersion         string          `json:"artifact_version"`
	ParentVersion           string          `json:"parent_version"`
	ParentSHA256            string          `json:"parent_sha256"`
	Patch                   MinimalPatch    `json:"patch"`
	PatchSHA256             string          `json:"patch_sha256"`
	TargetClusterID         string          `json:"target_cluster_id"`
	SourceProposalSHA256    string          `json:"source_proposal_sha256"`
	ProposerVersion         string          `json:"proposer_version"`
	ValidatorVersion        string          `json:"validator_version"`
	DataSplit               string          `json:"data_split"`
	DataVersion             string          `json:"data_version"`
	DataSHA256              string          `json:"data_sha256"`
	Budget                  ExecutionBudget `json:"budget"`
	Status                  string          `json:"status"`
	StaticValidationPassed  bool            `json:"static_validation_passed"`
	RequiresHumanApproval   bool            `json:"requires_human_approval"`
	OfflineEvaluationPassed bool            `json:"offline_evaluation_passed"`
	HoldoutOpened           bool            `json:"holdout_opened"`
	Applied                 bool            `json:"applied"`
	RollbackVersion         string          `json:"rollback_version"`
	ArtifactSHA256          string          `json:"artifact_sha256"`
	CreatedAt               time.Time       `json:"created_at"`
}

type SkipSummary struct {
	ProposalID string `json:"proposal_id"`
	ReasonCode string `json:"reason_code"`
}

type Audit struct {
	SchemaVersion  string         `json:"schema_version"`
	Mode           string         `json:"mode"`
	ArtifactCount  int64          `json:"artifact_count"`
	ReviewCount    int64          `json:"review_count"`
	ApprovedCount  int64          `json:"approved_count"`
	AppliedCount   int64          `json:"applied_count"`
	ActivePointers int            `json:"active_pointers"`
	Latest         []ArtifactView `json:"latest"`
	Guardrails     []string       `json:"guardrails"`
	Limitations    []string       `json:"limitations"`
}

type MaterializeResult struct {
	SchemaVersion string        `json:"schema_version"`
	SourceRunID   string        `json:"source_run_id"`
	ProposalCount int           `json:"proposal_count"`
	CreatedCount  int           `json:"created_count"`
	ExistingCount int           `json:"existing_count"`
	Skipped       []SkipSummary `json:"skipped"`
	Audit         Audit         `json:"audit"`
}
