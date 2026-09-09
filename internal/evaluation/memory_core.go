package evaluation

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const MemoryDatasetVersion = "devsupport-memory-v1"

var memoryCategories = []string{"relevant", "stale_wrong", "deleted", "cross_principal", "context_budget"}

type MemoryFactFixture struct {
	ID            string  `json:"id"`
	OwnerScope    string  `json:"owner_scope"`
	Key           string  `json:"key"`
	Value         string  `json:"value"`
	Status        string  `json:"status"`
	Confidence    float64 `json:"confidence"`
	Expiry        string  `json:"expiry"`
	ObservedOrder int     `json:"observed_order"`
}

type MemoryExpected struct {
	IncludedKeys    []string `json:"included_keys"`
	ForbiddenValues []string `json:"forbidden_values"`
}

type MemoryCase struct {
	ID             string              `json:"id"`
	Category       string              `json:"category"`
	Query          string              `json:"query"`
	Limit          int                 `json:"limit"`
	BudgetTokens   int                 `json:"budget_tokens"`
	Facts          []MemoryFactFixture `json:"facts"`
	DeletedIDs     []string            `json:"deleted_ids,omitempty"`
	Expected       MemoryExpected      `json:"expected"`
	ReviewedBy     string              `json:"reviewed_by"`
	DatasetVersion string              `json:"dataset_version"`
}

type MemoryDatasetSummary struct {
	CaseCount      int            `json:"case_count"`
	CategoryCounts map[string]int `json:"category_counts"`
	HumanReviewed  bool           `json:"human_reviewed"`
}

func LoadMemoryDataset(reader io.Reader) ([]MemoryCase, MemoryDatasetSummary, error) {
	if reader == nil {
		return nil, MemoryDatasetSummary{}, errors.New("memory dataset reader is required")
	}
	summary := MemoryDatasetSummary{CategoryCounts: make(map[string]int, len(memoryCategories)), HumanReviewed: true}
	cases := make([]MemoryCase, 0, 20)
	seen := make(map[string]struct{}, 20)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var item MemoryCase
		decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&item); err != nil {
			return nil, summary, fmt.Errorf("memory dataset line %d: %w", line, err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, summary, fmt.Errorf("memory dataset line %d: trailing JSON content", line)
		}
		if err := validateMemoryCase(item); err != nil {
			return nil, summary, fmt.Errorf("memory dataset line %d: %w", line, err)
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return nil, summary, fmt.Errorf("memory dataset line %d: duplicate id %q", line, item.ID)
		}
		seen[item.ID] = struct{}{}
		cases = append(cases, item)
		summary.CategoryCounts[item.Category]++
		if item.ReviewedBy != "human" {
			summary.HumanReviewed = false
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, summary, err
	}
	summary.CaseCount = len(cases)
	if len(cases) != 20 {
		return nil, summary, fmt.Errorf("memory dataset must contain 20 cases, got %d", len(cases))
	}
	for _, category := range memoryCategories {
		if summary.CategoryCounts[category] != 4 {
			return nil, summary, fmt.Errorf("memory category %q must contain 4 cases, got %d", category, summary.CategoryCounts[category])
		}
	}
	return cases, summary, nil
}

func validateMemoryCase(item MemoryCase) error {
	if err := validateDatasetText("id", item.ID, 1, 64); err != nil {
		return err
	}
	if !containsValue(memoryCategories, item.Category) {
		return fmt.Errorf("unsupported category %q", item.Category)
	}
	if err := validateDatasetText("query", item.Query, 1, 1000); err != nil {
		return err
	}
	if item.DatasetVersion != MemoryDatasetVersion {
		return fmt.Errorf("dataset_version must be %q", MemoryDatasetVersion)
	}
	if item.ReviewedBy != "human" && item.ReviewedBy != "pending_user" {
		return errors.New("reviewed_by must be human or pending_user")
	}
	if item.Limit < 1 || item.Limit > 5 || item.BudgetTokens < 64 || item.BudgetTokens > 8192 {
		return errors.New("memory limit or budget is out of bounds")
	}
	if len(item.Facts) > 16 || len(item.Expected.IncludedKeys) > 5 || len(item.Expected.ForbiddenValues) > 16 {
		return errors.New("memory fixture exceeds bounded counts")
	}
	factIDs := make(map[string]struct{}, len(item.Facts))
	for _, fact := range item.Facts {
		if err := validateDatasetText("fact id", fact.ID, 1, 64); err != nil {
			return err
		}
		if _, duplicate := factIDs[fact.ID]; duplicate {
			return fmt.Errorf("duplicate fact id %q", fact.ID)
		}
		factIDs[fact.ID] = struct{}{}
		if !containsValue([]string{"self", "other_user", "other_tenant"}, fact.OwnerScope) || !containsValue([]string{"active", "candidate", "conflicted", "superseded"}, fact.Status) || !containsValue([]string{"fresh", "expired", "none"}, fact.Expiry) {
			return fmt.Errorf("invalid fixture state for %q", fact.ID)
		}
		if fact.Confidence < 0 || fact.Confidence > 1 || fact.ObservedOrder < 0 || fact.ObservedOrder > 100 {
			return fmt.Errorf("invalid confidence or observed order for %q", fact.ID)
		}
		if err := validateDatasetText("fact key", fact.Key, 1, 64); err != nil {
			return err
		}
		if err := validateDatasetText("fact value", fact.Value, 1, 256); err != nil {
			return err
		}
	}
	for _, id := range item.DeletedIDs {
		if _, exists := factIDs[id]; !exists {
			return fmt.Errorf("deleted fact %q is missing", id)
		}
	}
	if err := validateDatasetList("included key", item.Expected.IncludedKeys, 0, 5, 64); err != nil {
		return err
	}
	return validateDatasetList("forbidden value", item.Expected.ForbiddenValues, 0, 16, 256)
}

func containsValue(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
