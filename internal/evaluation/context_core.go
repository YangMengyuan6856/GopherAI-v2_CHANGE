package evaluation

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	memorydomain "GopherAI/internal/memory"
)

const ContextDatasetVersion = "devsupport-context-compression-v1"

var contextOutcomes = []string{"answer", "clarify", "refuse", "resume"}

type ContextCase struct {
	ID              string                         `json:"id"`
	Outcome         string                         `json:"outcome"`
	BudgetTokens    int                            `json:"budget_tokens"`
	Summary         memorydomain.StructuredSummary `json:"summary"`
	NoiseTurns      int                            `json:"noise_turns"`
	NoiseText       string                         `json:"noise_text"`
	CurrentQuestion string                         `json:"current_question"`
	ReviewedBy      string                         `json:"reviewed_by"`
	DatasetVersion  string                         `json:"dataset_version"`
}

type ContextDatasetSummary struct {
	CaseCount     int            `json:"case_count"`
	OutcomeCounts map[string]int `json:"outcome_counts"`
	HumanReviewed bool           `json:"human_reviewed"`
}

func LoadContextDataset(reader io.Reader) ([]ContextCase, ContextDatasetSummary, error) {
	if reader == nil {
		return nil, ContextDatasetSummary{}, errors.New("context dataset reader is required")
	}
	summary := ContextDatasetSummary{OutcomeCounts: make(map[string]int, len(contextOutcomes)), HumanReviewed: true}
	cases, seen := make([]ContextCase, 0, 12), make(map[string]struct{}, 12)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var item ContextCase
		decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&item); err != nil {
			return nil, summary, fmt.Errorf("context dataset line %d: %w", line, err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, summary, fmt.Errorf("context dataset line %d: trailing JSON content", line)
		}
		if err := validateContextCase(item); err != nil {
			return nil, summary, fmt.Errorf("context dataset line %d: %w", line, err)
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return nil, summary, fmt.Errorf("context dataset line %d: duplicate id %q", line, item.ID)
		}
		seen[item.ID] = struct{}{}
		cases = append(cases, item)
		summary.OutcomeCounts[item.Outcome]++
		if item.ReviewedBy != "human" {
			summary.HumanReviewed = false
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, summary, err
	}
	summary.CaseCount = len(cases)
	if len(cases) != 12 {
		return nil, summary, fmt.Errorf("context dataset must contain 12 cases, got %d", len(cases))
	}
	for _, outcome := range contextOutcomes {
		if summary.OutcomeCounts[outcome] != 3 {
			return nil, summary, fmt.Errorf("context outcome %q must contain 3 cases, got %d", outcome, summary.OutcomeCounts[outcome])
		}
	}
	return cases, summary, nil
}

func validateContextCase(item ContextCase) error {
	if err := validateDatasetText("id", item.ID, 1, 64); err != nil {
		return err
	}
	if !containsValue(contextOutcomes, item.Outcome) {
		return fmt.Errorf("unsupported outcome %q", item.Outcome)
	}
	if item.DatasetVersion != ContextDatasetVersion {
		return fmt.Errorf("dataset_version must be %q", ContextDatasetVersion)
	}
	if item.ReviewedBy != "human" && item.ReviewedBy != "pending_user" {
		return errors.New("reviewed_by must be human or pending_user")
	}
	if item.BudgetTokens < 256 || item.BudgetTokens > 2048 || item.NoiseTurns < 4 || item.NoiseTurns > 20 {
		return errors.New("context budget or noise turns are out of bounds")
	}
	if err := validateDatasetText("noise_text", item.NoiseText, 20, 500); err != nil {
		return err
	}
	if err := validateDatasetText("current_question", item.CurrentQuestion, 1, 1000); err != nil {
		return err
	}
	if strings.TrimSpace(item.Summary.Goal) == "" || strings.TrimSpace(item.Summary.NextAction) == "" || len(item.Summary.Constraints) == 0 || len(item.Summary.ConfirmedFacts) == 0 || len(item.Summary.OpenQuestions) == 0 {
		return errors.New("context summary must cover goal, constraints, facts, open questions and next action")
	}
	return nil
}
