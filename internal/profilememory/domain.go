package profilememory

import (
	"errors"
	"time"
)

const (
	SchemaVersion = "profile-memory-v1"

	StatusCandidate  = "candidate"
	StatusActive     = "active"
	StatusConflicted = "conflicted"
	StatusSuperseded = "superseded"

	SourceDiagnosticObservation = "diagnostic_observation"
	SourceUserCorrected         = "user_corrected"

	CandidateTTL = 90 * 24 * time.Hour
	ConfirmedTTL = 180 * 24 * time.Hour
)

var (
	ErrInvalidProfileMemory  = errors.New("invalid profile memory")
	ErrProfileMemoryNotFound = errors.New("profile memory not found")
)

var allowedKeys = map[string]struct{}{
	"os": {}, "go_version": {}, "deployment_mode": {}, "cloud_provider": {}, "redis_version": {}, "mysql_version": {},
}

type PublicMemory struct {
	ID             string     `json:"id"`
	Key            string     `json:"key"`
	Value          string     `json:"value"`
	SourceType     string     `json:"source_type"`
	SourceRunID    string     `json:"source_run_id,omitempty"`
	Confidence     float64    `json:"confidence"`
	Status         string     `json:"status"`
	Version        int        `json:"version"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	LastObservedAt time.Time  `json:"last_observed_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type ListResponse struct {
	SchemaVersion  string         `json:"schema_version"`
	Items          []PublicMemory `json:"items"`
	ActiveCount    int            `json:"active_count"`
	CandidateCount int            `json:"candidate_count"`
	ConflictCount  int            `json:"conflict_count"`
}

type Correction struct {
	MemoryID      string
	UserID        string
	Value         string
	ExpiresInDays *int
}
