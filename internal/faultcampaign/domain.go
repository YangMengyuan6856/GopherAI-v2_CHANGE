package faultcampaign

import "time"

const (
	SchemaVersion      = "fault-injection-campaign-v1"
	FixtureVersion     = "devsupport-three-faults-v1"
	AcceptanceScenario = "three_failure_classes"
	Environment        = "isolated_acceptance_harness"
	Mode               = "observe_only"
)

type ServiceIndicators struct {
	QualityRate  float64 `json:"quality_rate"`
	SuccessRate  float64 `json:"success_rate"`
	P95LatencyMS int64   `json:"p95_latency_ms"`
	P99LatencyMS int64   `json:"p99_latency_ms"`
	InputTokens  int     `json:"input_tokens"`
	CostMicros   int64   `json:"cost_micros"`
	Population   int     `json:"population"`
}

type TimelinePoint struct {
	Phase                string            `json:"phase"`
	OffsetSeconds        int               `json:"offset_seconds"`
	MetricValue          float64           `json:"metric_value"`
	DecisionStatus       string            `json:"decision_status"`
	IncidentEvent        string            `json:"incident_event"`
	RecommendationAction string            `json:"recommendation_action"`
	WeightDeltaBasis     int               `json:"weight_delta_basis"`
	Applied              bool              `json:"applied"`
	TrafficChanged       bool              `json:"traffic_changed"`
	Indicators           ServiceIndicators `json:"indicators"`
}

type ScenarioReport struct {
	ScenarioID                 string          `json:"scenario_id"`
	Name                       string          `json:"name"`
	FaultClass                 string          `json:"fault_class"`
	InjectionMechanism         string          `json:"injection_mechanism"`
	InjectedOutcome            string          `json:"injected_outcome"`
	Metric                     string          `json:"metric"`
	Strategy                   string          `json:"strategy"`
	Direction                  string          `json:"direction"`
	Detected                   bool            `json:"detected"`
	Recovered                  bool            `json:"recovered"`
	FixedThresholdDetected     bool            `json:"fixed_threshold_detected"`
	ZScoreDetected             bool            `json:"z_score_detected"`
	MTTDSeconds                int             `json:"mttd_seconds"`
	RecommendationDelaySeconds int             `json:"recommendation_delay_seconds"`
	RecoverySeconds            int             `json:"recovery_seconds"`
	FalsePositiveChecks        int             `json:"false_positive_checks"`
	FalsePositives             int             `json:"false_positives"`
	RecommendationCount        int             `json:"recommendation_count"`
	AppliedCount               int             `json:"applied_count"`
	Timeline                   []TimelinePoint `json:"timeline"`
	EvidenceSHA256             string          `json:"evidence_sha256"`
}

type CampaignSummary struct {
	ScenarioCount          int     `json:"scenario_count"`
	DetectedCount          int     `json:"detected_count"`
	RecoveredCount         int     `json:"recovered_count"`
	RecommendationCount    int     `json:"recommendation_count"`
	AppliedCount           int     `json:"applied_count"`
	FalsePositiveChecks    int     `json:"false_positive_checks"`
	FalsePositives         int     `json:"false_positives"`
	DetectionRate          float64 `json:"detection_rate"`
	RecoveryRate           float64 `json:"recovery_rate"`
	FalsePositiveRate      float64 `json:"false_positive_rate"`
	MeanMTTDSeconds        float64 `json:"mean_mttd_seconds"`
	MeanRecoverySeconds    float64 `json:"mean_recovery_seconds"`
	MitigationSuccessState string  `json:"mitigation_success_state"`
}

type CampaignReport struct {
	SchemaVersion     string           `json:"schema_version"`
	FixtureVersion    string           `json:"fixture_version"`
	CampaignID        string           `json:"campaign_id"`
	ReportSHA256      string           `json:"report_sha256"`
	Environment       string           `json:"environment"`
	Mode              string           `json:"mode"`
	Simulation        bool             `json:"simulation"`
	AffectsProduction bool             `json:"affects_production"`
	Applied           bool             `json:"applied"`
	Summary           CampaignSummary  `json:"summary"`
	Scenarios         []ScenarioReport `json:"scenarios"`
	Guardrails        []string         `json:"guardrails"`
	Limitations       []string         `json:"limitations"`
}

type AuditSnapshot struct {
	SchemaVersion  string          `json:"schema_version"`
	FixtureVersion string          `json:"fixture_version"`
	Environment    string          `json:"environment"`
	Mode           string          `json:"mode"`
	RunCount       int64           `json:"run_count"`
	Latest         *CampaignReport `json:"latest,omitempty"`
	Guardrails     []string        `json:"guardrails"`
	Limitations    []string        `json:"limitations"`
}

type StoredCampaign struct {
	Report    CampaignReport
	CreatedAt time.Time
}
