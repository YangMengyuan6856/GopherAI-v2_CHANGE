package evaluation

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

const DiagnosticDatasetVersion = "devsupport-diagnostic-v1"

const (
	EvidenceSufficient   = "sufficient"
	EvidencePartial      = "partial"
	EvidenceInsufficient = "insufficient"
)

var diagnosticCategories = []string{
	"redis",
	"mysql",
	"http_proxy",
	"jwt_auth",
	"docker_network",
	"rabbitmq_indexing",
	"rag_citation",
	"frontend_sse",
}

var diagnosticSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`),
	regexp.MustCompile(`(?i)\b(?:password|passwd|secret|api[_-]?key|access[_-]?key|authorization)\s*[:=]\s*[^\s<][^\s]*`),
}

type DiagnosticContext struct {
	Environment []string `json:"environment,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

type DiagnosticExpected struct {
	RootCauses         []string `json:"root_causes"`
	NecessarySteps     []string `json:"necessary_steps"`
	VerificationAction string   `json:"verification_action"`
	ForbiddenClaims    []string `json:"forbidden_claims"`
	ForbiddenActions   []string `json:"forbidden_actions"`
	ShouldClarify      bool     `json:"should_clarify"`
}

type DiagnosticCase struct {
	ID                   string             `json:"id"`
	Category             string             `json:"category"`
	Question             string             `json:"question"`
	Context              DiagnosticContext  `json:"context,omitempty"`
	Expected             DiagnosticExpected `json:"expected"`
	EvidenceAvailability string             `json:"evidence_availability"`
	Difficulty           string             `json:"difficulty"`
	Tags                 []string           `json:"tags,omitempty"`
	ReviewedBy           string             `json:"reviewed_by"`
	DatasetVersion       string             `json:"dataset_version"`
}

type DiagnosticDatasetSummary struct {
	CaseCount          int            `json:"case_count"`
	CategoryCounts     map[string]int `json:"category_counts"`
	DifficultyCounts   map[string]int `json:"difficulty_counts"`
	EvidenceCounts     map[string]int `json:"evidence_counts"`
	ClarificationCount int            `json:"clarification_count"`
	HumanReviewed      bool           `json:"human_reviewed"`
}

func LoadDiagnosticDataset(reader io.Reader) ([]DiagnosticCase, DiagnosticDatasetSummary, error) {
	if reader == nil {
		return nil, DiagnosticDatasetSummary{}, errors.New("diagnostic dataset reader is required")
	}
	summary := DiagnosticDatasetSummary{
		CategoryCounts:   make(map[string]int, len(diagnosticCategories)),
		DifficultyCounts: make(map[string]int, 3),
		EvidenceCounts:   make(map[string]int, 3),
		HumanReviewed:    true,
	}
	cases := make([]DiagnosticCase, 0, 40)
	seen := make(map[string]struct{}, 40)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var item DiagnosticCase
		decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&item); err != nil {
			return nil, summary, fmt.Errorf("diagnostic dataset line %d: %w", line, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			if err == nil {
				err = errors.New("multiple JSON values")
			}
			return nil, summary, fmt.Errorf("diagnostic dataset line %d: %w", line, err)
		}
		if err := validateDiagnosticCase(item); err != nil {
			return nil, summary, fmt.Errorf("diagnostic dataset line %d: %w", line, err)
		}
		if _, exists := seen[item.ID]; exists {
			return nil, summary, fmt.Errorf("diagnostic dataset line %d: duplicate id %q", line, item.ID)
		}
		seen[item.ID] = struct{}{}
		cases = append(cases, item)
		summary.CategoryCounts[item.Category]++
		summary.DifficultyCounts[item.Difficulty]++
		summary.EvidenceCounts[item.EvidenceAvailability]++
		if item.Expected.ShouldClarify {
			summary.ClarificationCount++
		}
		if item.ReviewedBy != "human" {
			summary.HumanReviewed = false
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, summary, err
	}
	summary.CaseCount = len(cases)
	if len(cases) != 40 {
		return nil, summary, fmt.Errorf("diagnostic dataset must contain 40 cases, got %d", len(cases))
	}
	for _, category := range diagnosticCategories {
		if summary.CategoryCounts[category] != 5 {
			return nil, summary, fmt.Errorf("diagnostic category %q must contain 5 cases, got %d", category, summary.CategoryCounts[category])
		}
	}
	if summary.ClarificationCount < 8 {
		return nil, summary, fmt.Errorf("diagnostic dataset must contain at least 8 clarification cases, got %d", summary.ClarificationCount)
	}
	return cases, summary, nil
}

func validateDiagnosticCase(item DiagnosticCase) error {
	if err := validateDatasetText("id", item.ID, 1, 64); err != nil {
		return err
	}
	if !containsDiagnosticCategory(item.Category) {
		return fmt.Errorf("unsupported category %q", item.Category)
	}
	if err := validateDatasetText("question", item.Question, 1, 3000); err != nil {
		return err
	}
	if item.DatasetVersion != DiagnosticDatasetVersion {
		return fmt.Errorf("dataset_version must be %q", DiagnosticDatasetVersion)
	}
	if item.Difficulty != "easy" && item.Difficulty != "medium" && item.Difficulty != "hard" {
		return fmt.Errorf("invalid difficulty %q", item.Difficulty)
	}
	if item.ReviewedBy != "human" && item.ReviewedBy != "pending_user" {
		return errors.New("reviewed_by must be human or pending_user")
	}
	switch item.EvidenceAvailability {
	case EvidenceSufficient, EvidencePartial, EvidenceInsufficient:
	default:
		return fmt.Errorf("invalid evidence_availability %q", item.EvidenceAvailability)
	}
	if item.EvidenceAvailability == EvidenceInsufficient && !item.Expected.ShouldClarify {
		return errors.New("insufficient evidence case must require clarification")
	}
	if err := validateDatasetList("environment", item.Context.Environment, 0, 8, 300); err != nil {
		return err
	}
	if err := validateDatasetList("evidence_id", item.Context.EvidenceIDs, 0, 8, 128); err != nil {
		return err
	}
	if err := validateDatasetList("root_cause", item.Expected.RootCauses, 1, 3, 128); err != nil {
		return err
	}
	if err := validateDatasetList("necessary_step", item.Expected.NecessarySteps, 2, 4, 128); err != nil {
		return err
	}
	if err := validateDatasetText("verification_action", item.Expected.VerificationAction, 1, 300); err != nil {
		return err
	}
	if err := validateDatasetList("forbidden_claim", item.Expected.ForbiddenClaims, 1, 5, 200); err != nil {
		return err
	}
	if err := validateDatasetList("forbidden_action", item.Expected.ForbiddenActions, 1, 5, 200); err != nil {
		return err
	}
	if err := validateDatasetList("tag", item.Tags, 1, 8, 64); err != nil {
		return err
	}
	allText := []string{item.ID, item.Category, item.Question, item.Expected.VerificationAction, item.Difficulty, item.ReviewedBy, item.DatasetVersion}
	allText = append(allText, item.Context.Environment...)
	allText = append(allText, item.Context.EvidenceIDs...)
	allText = append(allText, item.Expected.RootCauses...)
	allText = append(allText, item.Expected.NecessarySteps...)
	allText = append(allText, item.Expected.ForbiddenClaims...)
	allText = append(allText, item.Expected.ForbiddenActions...)
	allText = append(allText, item.Tags...)
	for _, value := range allText {
		for _, pattern := range diagnosticSecretPatterns {
			if pattern.MatchString(value) {
				return errors.New("dataset contains credential-like material")
			}
		}
	}
	return nil
}

func validateDatasetList(name string, values []string, minimum int, maximum int, maxRunes int) error {
	if len(values) < minimum || len(values) > maximum {
		return fmt.Errorf("%s count must be between %d and %d", name, minimum, maximum)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := validateDatasetText(name, value, 1, maxRunes); err != nil {
			return err
		}
		normalized := strings.ToLower(value)
		if _, exists := seen[normalized]; exists {
			return fmt.Errorf("duplicate %s", name)
		}
		seen[normalized] = struct{}{}
	}
	return nil
}

func validateDatasetText(name string, value string, minimum int, maximum int) error {
	trimmed := strings.TrimSpace(value)
	length := utf8.RuneCountInString(trimmed)
	if value != trimmed || length < minimum || length > maximum {
		return fmt.Errorf("%s length or whitespace is invalid", name)
	}
	return nil
}

func containsDiagnosticCategory(candidate string) bool {
	for _, category := range diagnosticCategories {
		if candidate == category {
			return true
		}
	}
	return false
}
