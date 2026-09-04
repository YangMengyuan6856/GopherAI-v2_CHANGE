package incident

import (
	"errors"
	"time"

	"GopherAI/internal/diagnostic"
	"GopherAI/model"
)

const (
	SchemaVersion          = "resolved-incident-v1"
	ExtractorVersion       = "incident-extractor-v1"
	StatusConfirmed        = "confirmed"
	IndexStatusPending     = "pending"
	IndexStatusIndexed     = "indexed"
	IndexStatusFailed      = "failed"
	FeedbackConfirmed      = "resolution_confirmed"
	IndexTopic             = "gopher.incident.index.v1"
	IndexEventType         = "incident.index.requested"
	OutboxStatusPending    = "pending"
	ErrorEventInvalid      = "INCIDENT_EVENT_INVALID"
	ErrorIncidentNotFound  = "INCIDENT_NOT_FOUND"
	ErrorRedisIndex        = "INCIDENT_REDIS_INDEX_FAILED"
	ErrorIndexCompletion   = "INCIDENT_INDEX_COMPLETION_FAILED"
	DefaultMaximumAttempts = 3
)

var (
	ErrRunNotEligible      = errors.New("diagnostic run is not eligible for resolution confirmation")
	ErrHypothesisNotFound  = errors.New("diagnostic hypothesis was not found")
	ErrInvalidConfirmation = errors.New("resolution confirmation is invalid")
	ErrIdempotencyConflict = errors.New("resolution confirmation idempotency conflict")
	ErrAlreadyConfirmed    = errors.New("diagnostic run already has a different confirmed resolution")
)

type Proposal struct {
	SchemaVersion        string                         `json:"schema_version"`
	ProposalID           string                         `json:"proposal_id"`
	RunID                string                         `json:"run_id"`
	ExpectedStateVersion int64                          `json:"expected_state_version"`
	HypothesisID         string                         `json:"hypothesis_id"`
	Symptom              string                         `json:"symptom"`
	ProposedRootCause    string                         `json:"proposed_root_cause"`
	Evidence             []diagnostic.EvidenceReference `json:"evidence"`
	VerificationSteps    []diagnostic.VerificationStep  `json:"verification_steps"`
	RequiresHumanConfirm bool                           `json:"requires_human_confirm"`
}

type ConfirmCommand struct {
	RunID                string
	UserID               string
	HypothesisID         string
	Resolution           string
	ClientRequestID      string
	ExpectedStateVersion int64
}

type Confirmation struct {
	SchemaVersion string                 `json:"schema_version"`
	Created       bool                   `json:"created"`
	Incident      PublicResolvedIncident `json:"incident"`
}

type PublicResolvedIncident struct {
	ID              string     `json:"id"`
	SourceRunID     string     `json:"source_run_id"`
	SessionID       string     `json:"session_id,omitempty"`
	HypothesisID    string     `json:"hypothesis_id"`
	Symptom         string     `json:"symptom"`
	RootCause       string     `json:"root_cause"`
	Resolution      string     `json:"resolution"`
	Components      []string   `json:"components"`
	ErrorSignatures []string   `json:"error_signatures"`
	Status          string     `json:"status"`
	IndexStatus     string     `json:"index_status"`
	ConfirmedAt     time.Time  `json:"confirmed_at"`
	IndexedAt       *time.Time `json:"indexed_at,omitempty"`
}

type confirmationWrite struct {
	Run              model.AgentLifecycleRun
	Feedback         model.ResolutionFeedback
	Incident         model.ResolvedIncident
	Event            model.OutboxEvent
	ResolutionSHA256 string
}
