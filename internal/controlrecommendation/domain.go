package controlrecommendation

import "time"

const (
	SchemaVersion               = "control-recommendation-v1"
	ModeRecommendOnly           = "recommend_only"
	StatusRecommended           = "recommended"
	StatusBlocked               = "blocked"
	ReasonCandidateCreated      = "candidate_created"
	ReasonBaselineNotEligible   = "baseline_not_eligible"
	ReasonStrategyNotInPolicy   = "strategy_not_in_active_policy"
	ReasonNoFallback            = "healthy_fallback_unavailable"
	ReasonExplorationFloor      = "exploration_floor_guard"
	ReasonInvalidRecommendation = "invalid_detector_recommendation"
	AcceptanceScenario          = "recommend_only_guardrails"
)

type EvaluationGate struct {
	Source               string `json:"source"`
	RunID                string `json:"run_id"`
	CandidateVersion     string `json:"candidate_version"`
	ReportSHA256         string `json:"report_sha256"`
	TechnicalGatesPassed bool   `json:"technical_gates_passed"`
	HumanReviewed        bool   `json:"human_reviewed"`
	BaselineEligible     bool   `json:"baseline_eligible"`
}

type RecommendationSummary struct {
	RecommendationID       string    `json:"recommendation_id"`
	Status                 string    `json:"status"`
	Simulation             bool      `json:"simulation"`
	Source                 string    `json:"source"`
	Metric                 string    `json:"metric"`
	Strategy               string    `json:"strategy"`
	Intent                 string    `json:"intent,omitempty"`
	ReasonCode             string    `json:"reason_code"`
	ParentPolicyVersion    string    `json:"parent_policy_version"`
	ParentPolicySHA256     string    `json:"parent_policy_sha256"`
	CandidatePolicyVersion string    `json:"candidate_policy_version,omitempty"`
	CandidatePolicySHA256  string    `json:"candidate_policy_sha256,omitempty"`
	BeforeWeightBasis      int       `json:"before_weight_basis"`
	ProposedWeightBasis    int       `json:"proposed_weight_basis"`
	FallbackStrategy       string    `json:"fallback_strategy,omitempty"`
	FallbackBeforeBasis    int       `json:"fallback_before_basis"`
	FallbackProposedBasis  int       `json:"fallback_proposed_basis"`
	WeightDeltaBasis       int       `json:"weight_delta_basis"`
	EvidenceSHA256         string    `json:"evidence_sha256"`
	EvaluationRunID        string    `json:"evaluation_run_id,omitempty"`
	EvaluationReportSHA256 string    `json:"evaluation_report_sha256,omitempty"`
	BaselineEligible       bool      `json:"baseline_eligible"`
	Applied                bool      `json:"applied"`
	CreatedAt              time.Time `json:"created_at"`
}

type AuditSnapshot struct {
	SchemaVersion string                  `json:"schema_version"`
	Mode          string                  `json:"mode"`
	Recommended   int64                   `json:"recommended"`
	Blocked       int64                   `json:"blocked"`
	ActivePolicy  PolicyIdentity          `json:"active_policy"`
	Evaluation    EvaluationGate          `json:"evaluation"`
	Latest        []RecommendationSummary `json:"latest"`
	Guardrails    []string                `json:"guardrails"`
	Limitations   []string                `json:"limitations"`
}

type PolicyIdentity struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Status  string `json:"status"`
}

type AcceptanceResult struct {
	SchemaVersion         string                `json:"schema_version"`
	Simulation            bool                  `json:"simulation"`
	Scenario              string                `json:"scenario"`
	BaselineGuard         RecommendationSummary `json:"baseline_guard"`
	EligibleFixture       RecommendationSummary `json:"eligible_fixture"`
	ActivePolicyBefore    PolicyIdentity        `json:"active_policy_before"`
	ActivePolicyAfter     PolicyIdentity        `json:"active_policy_after"`
	ActivePolicyUnchanged bool                  `json:"active_policy_unchanged"`
	Guardrails            []string              `json:"guardrails"`
}
