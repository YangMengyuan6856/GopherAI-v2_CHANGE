package model

import "time"

// OnlineEvaluationSample is a privacy-bounded snapshot selected from live
// traffic. Raw user and tenant identifiers are never stored in this table.
type OnlineEvaluationSample struct {
	ID                  string     `gorm:"primaryKey;type:char(36)" json:"id"`
	EventID             string     `gorm:"uniqueIndex;not null;type:char(36)" json:"event_id"`
	SchemaVersion       string     `gorm:"not null;type:varchar(64)" json:"schema_version"`
	SamplerVersion      string     `gorm:"not null;type:varchar(64)" json:"sampler_version"`
	EvaluatorVersion    string     `gorm:"not null;type:varchar(64)" json:"evaluator_version"`
	RequestHash         string     `gorm:"index;not null;type:char(64)" json:"request_hash"`
	TraceHash           string     `gorm:"index;not null;type:char(64)" json:"trace_hash"`
	UserHash            string     `gorm:"index;not null;type:char(64)" json:"user_hash"`
	TenantHash          string     `gorm:"index;not null;type:char(64)" json:"tenant_hash"`
	Intent              string     `gorm:"index;not null;type:varchar(64)" json:"intent"`
	IntentVersion       string     `gorm:"not null;type:varchar(64)" json:"intent_version"`
	Strategy            string     `gorm:"index;not null;type:varchar(64)" json:"strategy"`
	StrategyVersion     string     `gorm:"not null;type:varchar(64)" json:"strategy_version"`
	PolicyVersion       string     `gorm:"index;not null;type:varchar(64)" json:"policy_version"`
	TrafficClass        string     `gorm:"index;not null;type:varchar(16)" json:"traffic_class"`
	SampleRateBasis     int        `gorm:"not null" json:"sample_rate_basis"`
	SampleBucket        int        `gorm:"not null" json:"sample_bucket"`
	SampleReasonsJSON   string     `gorm:"type:text;not null" json:"-"`
	Question            string     `gorm:"type:text;not null" json:"-"`
	Answer              string     `gorm:"type:longtext;not null" json:"-"`
	EvidenceJSON        string     `gorm:"type:longtext;not null" json:"-"`
	RedactionCount      int        `gorm:"not null;default:0" json:"redaction_count"`
	PayloadHash         string     `gorm:"not null;type:char(64)" json:"payload_hash"`
	ResponseConfidence  float64    `gorm:"not null;default:0" json:"response_confidence"`
	Resolved            bool       `gorm:"not null;default:false" json:"resolved"`
	ResponseErrorCode   string     `gorm:"type:varchar(64)" json:"response_error_code,omitempty"`
	Status              string     `gorm:"index;not null;type:varchar(32)" json:"status"`
	Attempt             int        `gorm:"not null;default:0" json:"attempt"`
	LastErrorCode       string     `gorm:"type:varchar(64)" json:"last_error_code,omitempty"`
	JudgeAdapterVersion string     `gorm:"type:varchar(64)" json:"judge_adapter_version,omitempty"`
	JudgePromptVersion  string     `gorm:"type:varchar(64)" json:"judge_prompt_version,omitempty"`
	JudgeModelVersion   string     `gorm:"type:varchar(128)" json:"judge_model_version,omitempty"`
	Relevance           float64    `gorm:"not null;default:0" json:"relevance,omitempty"`
	Completeness        float64    `gorm:"not null;default:0" json:"completeness,omitempty"`
	Helpfulness         float64    `gorm:"not null;default:0" json:"helpfulness,omitempty"`
	Groundedness        float64    `gorm:"not null;default:0" json:"groundedness,omitempty"`
	Safety              float64    `gorm:"not null;default:0" json:"safety,omitempty"`
	Overall             float64    `gorm:"not null;default:0" json:"overall,omitempty"`
	JudgeResultJSON     string     `gorm:"type:longtext" json:"-"`
	Simulation          bool       `gorm:"index;not null;default:false" json:"simulation"`
	EvaluatedAt         *time.Time `json:"evaluated_at,omitempty"`
	ExpiresAt           time.Time  `gorm:"index;not null" json:"expires_at"`
	CreatedAt           time.Time  `gorm:"index;not null" json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}
