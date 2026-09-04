package evaluation

import (
	"GopherAI/internal/intent"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const IntentDatasetVersion = "devsupport-intent-v1"

type IntentContext struct {
	PreviousIntent string `json:"previous_intent,omitempty"`
}

type IntentExpected struct {
	Intent     string `json:"intent"`
	IsCompound bool   `json:"is_compound"`
}

type IntentCase struct {
	ID             string         `json:"id"`
	Question       string         `json:"question"`
	Context        IntentContext  `json:"context,omitempty"`
	Expected       IntentExpected `json:"expected"`
	Difficulty     string         `json:"difficulty"`
	Tags           []string       `json:"tags,omitempty"`
	ReviewedBy     string         `json:"reviewed_by"`
	DatasetVersion string         `json:"dataset_version"`
}

type IntentDatasetSummary struct {
	CaseCount       int            `json:"case_count"`
	LabelCounts     map[string]int `json:"label_counts"`
	DifficultyCount map[string]int `json:"difficulty_counts"`
	CompoundCount   int            `json:"compound_count"`
	HumanReviewed   bool           `json:"human_reviewed"`
}

func LoadIntentDataset(reader io.Reader) ([]IntentCase, IntentDatasetSummary, error) {
	if reader == nil {
		return nil, IntentDatasetSummary{}, errors.New("intent dataset reader is required")
	}
	cases := make([]IntentCase, 0, 150)
	seen := make(map[string]struct{}, 150)
	summary := IntentDatasetSummary{LabelCounts: make(map[string]int, 6), DifficultyCount: make(map[string]int, 3), HumanReviewed: true}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var item IntentCase
		if err := json.Unmarshal([]byte(text), &item); err != nil {
			return nil, summary, fmt.Errorf("intent dataset line %d: %w", line, err)
		}
		if err := validateIntentCase(item); err != nil {
			return nil, summary, fmt.Errorf("intent dataset line %d: %w", line, err)
		}
		if _, exists := seen[item.ID]; exists {
			return nil, summary, fmt.Errorf("intent dataset line %d: duplicate id %q", line, item.ID)
		}
		seen[item.ID] = struct{}{}
		cases = append(cases, item)
		summary.LabelCounts[item.Expected.Intent]++
		summary.DifficultyCount[item.Difficulty]++
		if item.Expected.IsCompound {
			summary.CompoundCount++
		}
		if item.ReviewedBy != "human" {
			summary.HumanReviewed = false
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, summary, err
	}
	summary.CaseCount = len(cases)
	if len(cases) != 150 {
		return nil, summary, fmt.Errorf("intent dataset must contain 150 cases, got %d", len(cases))
	}
	for _, label := range intent.Labels() {
		if summary.LabelCounts[label] != 25 {
			return nil, summary, fmt.Errorf("intent label %q must contain 25 cases, got %d", label, summary.LabelCounts[label])
		}
	}
	return cases, summary, nil
}

func validateIntentCase(item IntentCase) error {
	switch {
	case strings.TrimSpace(item.ID) == "":
		return errors.New("id is required")
	case strings.TrimSpace(item.Question) == "":
		return errors.New("question is required")
	case item.DatasetVersion != IntentDatasetVersion:
		return fmt.Errorf("dataset_version must be %q", IntentDatasetVersion)
	case !intent.IsKnown(item.Expected.Intent):
		return fmt.Errorf("unknown expected intent %q", item.Expected.Intent)
	case item.Difficulty != "easy" && item.Difficulty != "medium" && item.Difficulty != "hard":
		return fmt.Errorf("invalid difficulty %q", item.Difficulty)
	case item.ReviewedBy != "human" && item.ReviewedBy != "pending_user":
		return errors.New("reviewed_by must be human or pending_user")
	case item.Expected.Intent == intent.FollowUp && !intent.IsKnown(item.Context.PreviousIntent):
		return errors.New("follow_up case requires a known previous_intent")
	case item.Expected.Intent == intent.FollowUp && (item.Context.PreviousIntent == intent.FollowUp || item.Context.PreviousIntent == intent.General):
		return errors.New("follow_up previous_intent must be a resolvable primary intent")
	}
	return nil
}
