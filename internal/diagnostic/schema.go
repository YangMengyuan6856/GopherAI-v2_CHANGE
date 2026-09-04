package diagnostic

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	SchemaVersion = "diagnostic-schema-v1"

	ConclusionHypothesis   = "hypothesis"
	ConclusionConfirmed    = "confirmed"
	ConclusionInsufficient = "insufficient"

	EvidenceUserObservation = "user_observation"
	EvidenceProjectDocument = "project_document"
	EvidenceResolvedCase    = "resolved_incident"

	ActionInspect   = "inspect"
	ActionQuery     = "query"
	ActionCompare   = "compare"
	ActionUserCheck = "user_check"

	CaseMemoryHit         = "hit"
	CaseMemoryNoMatch     = "no_match"
	CaseMemoryUnavailable = "unavailable"
	CaseMemoryPolicyV1    = "case-recall-v1"
)

var ErrInvalidDiagnostic = errors.New("invalid diagnostic result")

type EnvironmentFact struct {
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Source     string  `json:"source"`
	Confidence float64 `json:"confidence"`
}

type EvidenceReference struct {
	ID         string `json:"id"`
	SourceType string `json:"source_type"`
	Summary    string `json:"summary"`
}

type VerificationStep struct {
	ID                  string `json:"id"`
	ActionType          string `json:"action_type"`
	Instruction         string `json:"instruction"`
	ExpectedObservation string `json:"expected_observation"`
	FailureMeaning      string `json:"failure_meaning"`
	ReadOnly            bool   `json:"read_only"`
}

type Hypothesis struct {
	ID                string              `json:"id"`
	Cause             string              `json:"cause"`
	Confidence        float64             `json:"confidence"`
	Rationale         string              `json:"rationale"`
	Evidence          []EvidenceReference `json:"evidence"`
	VerificationSteps []VerificationStep  `json:"verification_steps"`
}

type MissingInformation struct {
	Field    string `json:"field"`
	Question string `json:"question"`
	Critical bool   `json:"critical"`
}

// SimilarIncident is a user-confirmed historical case. It is deliberately
// separated from EvidenceReference: past experience may suggest where to look,
// but it is not evidence that the current incident has the same root cause.
type SimilarIncident struct {
	IncidentID             string    `json:"incident_id"`
	Symptom                string    `json:"symptom"`
	RootCause              string    `json:"root_cause"`
	Resolution             string    `json:"resolution"`
	MatchedErrorSignatures []string  `json:"matched_error_signatures"`
	MatchedComponents      []string  `json:"matched_components"`
	Score                  float64   `json:"score"`
	ConfirmedAt            time.Time `json:"confirmed_at"`
}

type Result struct {
	Version              string               `json:"version"`
	Symptom              string               `json:"symptom"`
	Components           []string             `json:"components"`
	ErrorSignatures      []string             `json:"error_signatures"`
	KnownEnvironment     []EnvironmentFact    `json:"known_environment"`
	MissingInformation   []MissingInformation `json:"missing_information"`
	Hypotheses           []Hypothesis         `json:"hypotheses"`
	ConclusionStatus     string               `json:"conclusion_status"`
	ConfirmedRootCauseID string               `json:"confirmed_root_cause_id,omitempty"`
	NeedsUserInput       bool                 `json:"needs_user_input"`
	CaseMemoryStatus     string               `json:"case_memory_status,omitempty"`
	CaseMemoryPolicy     string               `json:"case_memory_policy,omitempty"`
	SimilarIncidents     []SimilarIncident    `json:"similar_incidents,omitempty"`
}

func (result Result) Validate() error {
	if result.Version != SchemaVersion {
		return invalid("version must be %q", SchemaVersion)
	}
	if err := validateText("symptom", result.Symptom, 1, 1000); err != nil {
		return err
	}
	if len(result.Components) > 8 || len(result.ErrorSignatures) > 12 || len(result.KnownEnvironment) > 16 || len(result.MissingInformation) > 8 || len(result.Hypotheses) > 3 {
		return invalid("collection bound exceeded")
	}
	if err := validateUniqueText("component", result.Components, 64); err != nil {
		return err
	}
	if err := validateUniqueText("error_signature", result.ErrorSignatures, 256); err != nil {
		return err
	}
	for _, fact := range result.KnownEnvironment {
		if err := fact.validate(); err != nil {
			return err
		}
	}
	for _, missing := range result.MissingInformation {
		if err := missing.validate(); err != nil {
			return err
		}
	}
	if err := result.validateCaseMemory(); err != nil {
		return err
	}
	if !sort.SliceIsSorted(result.Hypotheses, func(i, j int) bool { return result.Hypotheses[i].Confidence > result.Hypotheses[j].Confidence }) {
		return invalid("hypotheses must be ordered by descending confidence")
	}
	seenHypotheses := make(map[string]struct{}, len(result.Hypotheses))
	for _, hypothesis := range result.Hypotheses {
		if _, exists := seenHypotheses[hypothesis.ID]; exists {
			return invalid("duplicate hypothesis id")
		}
		seenHypotheses[hypothesis.ID] = struct{}{}
		if err := hypothesis.validate(); err != nil {
			return err
		}
	}
	switch result.ConclusionStatus {
	case ConclusionHypothesis:
		if len(result.Hypotheses) == 0 || result.ConfirmedRootCauseID != "" {
			return invalid("hypothesis conclusion requires candidates and no confirmed root cause")
		}
	case ConclusionConfirmed:
		if result.ConfirmedRootCauseID == "" {
			return invalid("confirmed conclusion requires a root cause id")
		}
		if _, exists := seenHypotheses[result.ConfirmedRootCauseID]; !exists {
			return invalid("confirmed root cause must reference a returned hypothesis")
		}
	case ConclusionInsufficient:
		if result.ConfirmedRootCauseID != "" || len(result.MissingInformation) == 0 || !result.NeedsUserInput {
			return invalid("insufficient conclusion requires missing information and user input")
		}
	default:
		return invalid("unsupported conclusion status")
	}
	return nil
}

func (result Result) validateCaseMemory() error {
	if result.CaseMemoryStatus == "" {
		if len(result.SimilarIncidents) != 0 || result.CaseMemoryPolicy != "" {
			return invalid("case memory status is required when similar incidents are present")
		}
		return nil
	}
	if result.CaseMemoryPolicy != CaseMemoryPolicyV1 {
		return invalid("unsupported case memory policy")
	}
	switch result.CaseMemoryStatus {
	case CaseMemoryHit:
		if len(result.SimilarIncidents) == 0 || len(result.SimilarIncidents) > 3 {
			return invalid("case memory hit requires 1 to 3 incidents")
		}
	case CaseMemoryNoMatch, CaseMemoryUnavailable:
		if len(result.SimilarIncidents) != 0 {
			return invalid("case memory without a hit cannot return incidents")
		}
	default:
		return invalid("unsupported case memory status")
	}
	for index, item := range result.SimilarIncidents {
		if err := validateText("similar incident id", item.IncidentID, 1, 64); err != nil {
			return err
		}
		if err := validateText("similar incident symptom", item.Symptom, 1, 1000); err != nil {
			return err
		}
		if err := validateText("similar incident root cause", item.RootCause, 1, 500); err != nil {
			return err
		}
		if err := validateText("similar incident resolution", item.Resolution, 1, 1000); err != nil {
			return err
		}
		if item.Score < 0 || item.Score > 1 || item.ConfirmedAt.IsZero() {
			return invalid("similar incident score or confirmation time is invalid")
		}
		if index > 0 && result.SimilarIncidents[index-1].Score < item.Score {
			return invalid("similar incidents must be ordered by descending score")
		}
		if err := validateUniqueText("matched error signature", item.MatchedErrorSignatures, 256); err != nil {
			return err
		}
		if err := validateUniqueText("matched component", item.MatchedComponents, 64); err != nil {
			return err
		}
	}
	return nil
}

func (fact EnvironmentFact) validate() error {
	if err := validateText("environment key", fact.Key, 1, 64); err != nil {
		return err
	}
	if err := validateText("environment value", fact.Value, 1, 256); err != nil {
		return err
	}
	if err := validateText("environment source", fact.Source, 1, 128); err != nil {
		return err
	}
	if fact.Confidence < 0 || fact.Confidence > 1 {
		return invalid("environment confidence must be between 0 and 1")
	}
	return nil
}

func (missing MissingInformation) validate() error {
	if err := validateText("missing field", missing.Field, 1, 64); err != nil {
		return err
	}
	return validateText("missing question", missing.Question, 1, 300)
}

func (hypothesis Hypothesis) validate() error {
	if err := validateText("hypothesis id", hypothesis.ID, 1, 64); err != nil {
		return err
	}
	if err := validateText("hypothesis cause", hypothesis.Cause, 1, 500); err != nil {
		return err
	}
	if err := validateText("hypothesis rationale", hypothesis.Rationale, 1, 1000); err != nil {
		return err
	}
	if hypothesis.Confidence < 0 || hypothesis.Confidence > 1 {
		return invalid("hypothesis confidence must be between 0 and 1")
	}
	if len(hypothesis.Evidence) == 0 || len(hypothesis.Evidence) > 10 {
		return invalid("hypothesis must reference 1 to 10 evidence items")
	}
	if len(hypothesis.VerificationSteps) == 0 || len(hypothesis.VerificationSteps) > 5 {
		return invalid("hypothesis must contain 1 to 5 verification steps")
	}
	for _, evidence := range hypothesis.Evidence {
		if err := evidence.validate(); err != nil {
			return err
		}
	}
	seenSteps := make(map[string]struct{}, len(hypothesis.VerificationSteps))
	for _, step := range hypothesis.VerificationSteps {
		if _, exists := seenSteps[step.ID]; exists {
			return invalid("duplicate verification step id")
		}
		seenSteps[step.ID] = struct{}{}
		if err := step.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (evidence EvidenceReference) validate() error {
	if err := validateText("evidence id", evidence.ID, 1, 128); err != nil {
		return err
	}
	if err := validateText("evidence summary", evidence.Summary, 1, 500); err != nil {
		return err
	}
	switch evidence.SourceType {
	case EvidenceUserObservation, EvidenceProjectDocument, EvidenceResolvedCase:
		return nil
	default:
		return invalid("unsupported evidence source type")
	}
}

func (step VerificationStep) validate() error {
	if err := validateText("verification step id", step.ID, 1, 64); err != nil {
		return err
	}
	if err := validateText("verification instruction", step.Instruction, 1, 500); err != nil {
		return err
	}
	if err := validateText("expected observation", step.ExpectedObservation, 1, 500); err != nil {
		return err
	}
	if err := validateText("failure meaning", step.FailureMeaning, 1, 500); err != nil {
		return err
	}
	if !step.ReadOnly {
		return invalid("verification steps must be read-only")
	}
	switch step.ActionType {
	case ActionInspect, ActionQuery, ActionCompare, ActionUserCheck:
		return nil
	default:
		return invalid("unsupported verification action type")
	}
}

func validateUniqueText(name string, values []string, maximum int) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := validateText(name, value, 1, maximum); err != nil {
			return err
		}
		normalized := strings.ToLower(strings.TrimSpace(value))
		if _, exists := seen[normalized]; exists {
			return invalid("duplicate %s", name)
		}
		seen[normalized] = struct{}{}
	}
	return nil
}

func validateText(name string, value string, minimum int, maximum int) error {
	trimmed := strings.TrimSpace(value)
	length := utf8.RuneCountInString(trimmed)
	if trimmed != value || length < minimum || length > maximum {
		return invalid("%s length or whitespace is invalid", name)
	}
	return nil
}

func invalid(format string, values ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidDiagnostic, fmt.Sprintf(format, values...))
}
