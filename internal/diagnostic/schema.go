package diagnostic

import (
	"errors"
	"fmt"
	"sort"
	"strings"
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
	if err := validateText("hypothesis id", hypothesis.ID, 1, 32); err != nil {
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
	if err := validateText("verification step id", step.ID, 1, 32); err != nil {
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
