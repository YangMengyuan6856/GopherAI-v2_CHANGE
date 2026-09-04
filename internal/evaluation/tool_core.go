package evaluation

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"GopherAI/internal/toolagent"
)

const ToolDatasetVersion = "devsupport-tool-runtime-v1"

var toolEvaluationCategories = []string{"selection", "schema", "authorization", "resilience", "safety"}

var toolEvaluationScenarios = map[string]string{
	"manifest_selection": "selection", "backend_selection": "selection", "worker_selection": "selection", "official_document_selection": "selection", "no_tool_selection": "selection", "log_signature_selection": "selection",
	"manifest_valid": "schema", "official_document_valid": "schema", "manifest_unknown_path": "schema", "health_bad_enum": "schema", "schema_repair_bounded": "schema", "health_missing_required": "schema",
	"hitl_confirm_allowed": "authorization", "permission_denied": "authorization", "intent_denied": "authorization", "hitl_confirmation_readonly_denied": "authorization", "budget_zero": "authorization", "budget_exhausted": "authorization",
	"retry_then_success": "resilience", "non_retryable_error": "resilience", "timeout": "resilience", "request_cancelled": "resilience", "circuit_open": "resilience", "cache_stale_fallback": "resilience",
	"duplicate_action_no_progress": "safety", "dangerous_database_write": "safety", "unknown_tool": "safety", "oversized_result": "safety", "cache_principal_isolation": "safety", "external_write_denied": "safety",
}

type ToolExpected struct {
	Decision          string   `json:"decision,omitempty"`
	ToolNames         []string `json:"tool_names,omitempty"`
	Status            string   `json:"status,omitempty"`
	ErrorCode         string   `json:"error_code,omitempty"`
	Cached            bool     `json:"cached"`
	Stale             bool     `json:"stale"`
	DegradedReason    string   `json:"degraded_reason,omitempty"`
	Executions        int      `json:"executions"`
	AuditCount        int      `json:"audit_count"`
	RepairCount       int      `json:"repair_count,omitempty"`
	TerminationReason string   `json:"termination_reason,omitempty"`
}

type ToolEvaluationCase struct {
	ID             string          `json:"id"`
	Category       string          `json:"category"`
	Scenario       string          `json:"scenario"`
	Message        string          `json:"message,omitempty"`
	Arguments      json.RawMessage `json:"arguments,omitempty"`
	Expected       ToolExpected    `json:"expected"`
	ReviewedBy     string          `json:"reviewed_by"`
	DatasetVersion string          `json:"dataset_version"`
}

type ToolDatasetSummary struct {
	CaseCount      int            `json:"case_count"`
	CategoryCounts map[string]int `json:"category_counts"`
	HumanReviewed  bool           `json:"human_reviewed"`
}

func LoadToolDataset(reader io.Reader) ([]ToolEvaluationCase, ToolDatasetSummary, error) {
	if reader == nil {
		return nil, ToolDatasetSummary{}, errors.New("tool evaluation dataset reader is required")
	}
	summary := ToolDatasetSummary{CategoryCounts: make(map[string]int, len(toolEvaluationCategories)), HumanReviewed: true}
	cases := make([]ToolEvaluationCase, 0, 30)
	seen := make(map[string]struct{}, 30)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var item ToolEvaluationCase
		decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&item); err != nil {
			return nil, summary, fmt.Errorf("tool evaluation dataset line %d: %w", line, err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, summary, fmt.Errorf("tool evaluation dataset line %d: trailing JSON content", line)
		}
		if err := validateToolEvaluationCase(item); err != nil {
			return nil, summary, fmt.Errorf("tool evaluation dataset line %d: %w", line, err)
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return nil, summary, fmt.Errorf("tool evaluation dataset line %d: duplicate id %q", line, item.ID)
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
	if len(cases) != 30 {
		return nil, summary, fmt.Errorf("tool evaluation dataset must contain 30 cases, got %d", len(cases))
	}
	for _, category := range toolEvaluationCategories {
		if summary.CategoryCounts[category] != 6 {
			return nil, summary, fmt.Errorf("tool evaluation category %q must contain 6 cases, got %d", category, summary.CategoryCounts[category])
		}
	}
	return cases, summary, nil
}

func validateToolEvaluationCase(item ToolEvaluationCase) error {
	if err := validateDatasetText("id", item.ID, 1, 64); err != nil {
		return err
	}
	if !containsValue(toolEvaluationCategories, item.Category) {
		return fmt.Errorf("unsupported category %q", item.Category)
	}
	if err := validateDatasetText("scenario", item.Scenario, 1, 64); err != nil {
		return err
	}
	if category, ok := toolEvaluationScenarios[item.Scenario]; !ok || category != item.Category {
		return fmt.Errorf("scenario %q does not belong to category %q", item.Scenario, item.Category)
	}
	if len(item.Message) > 2000 || len(item.Arguments) > 4096 {
		return errors.New("tool evaluation input exceeds bounded length")
	}
	if item.Expected.Executions < 0 || item.Expected.Executions > 4 || item.Expected.AuditCount < 0 || item.Expected.AuditCount > 4 || item.Expected.RepairCount < 0 || item.Expected.RepairCount > toolagent.MaxRepairAttempts || len(item.Expected.ToolNames) > 2 {
		return errors.New("tool evaluation expected counters are outside bounds")
	}
	if item.ReviewedBy != "human" && item.ReviewedBy != "pending_user" {
		return errors.New("reviewed_by must be human or pending_user")
	}
	if item.DatasetVersion != ToolDatasetVersion {
		return fmt.Errorf("dataset_version must be %q", ToolDatasetVersion)
	}
	return nil
}
