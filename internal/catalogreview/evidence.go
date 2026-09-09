package catalogreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const EvidenceSchemaVersion = "evaluation-catalog-review-evidence-v2"

type EvidenceEntry struct {
	CaseID       string    `json:"case_id"`
	Slice        string    `json:"slice"`
	CaseSHA256   string    `json:"case_sha256"`
	Revision     int       `json:"revision"`
	Decision     string    `json:"decision"`
	ReasonCodes  []string  `json:"reason_codes"`
	ReviewSHA256 string    `json:"review_sha256"`
	ReviewedAt   time.Time `json:"reviewed_at"`
}

type EvidenceSnapshot struct {
	SchemaVersion                 string          `json:"schema_version"`
	DatasetVersion                string          `json:"dataset_version"`
	CatalogSHA256                 string          `json:"catalog_sha256"`
	GovernanceSHA256              string          `json:"governance_sha256"`
	ReviewerScope                 string          `json:"reviewer_scope"`
	Status                        string          `json:"status"`
	TotalCases                    int             `json:"total_cases"`
	ReviewedCases                 int             `json:"reviewed_cases"`
	ApprovedCases                 int             `json:"approved_cases"`
	RejectedCases                 int             `json:"rejected_cases"`
	PendingCases                  int             `json:"pending_cases"`
	ReviewSetSHA256               string          `json:"review_set_sha256"`
	ReadyForSealedMaterialization bool            `json:"ready_for_sealed_materialization"`
	LastReviewedAt                *time.Time      `json:"last_reviewed_at,omitempty"`
	Entries                       []EvidenceEntry `json:"entries"`
	Guardrails                    []string        `json:"guardrails"`
	Limitations                   []string        `json:"limitations"`
	SnapshotSHA256                string          `json:"snapshot_sha256"`
}

func (service *Service) Evidence(ctx context.Context, reviewer string) (EvidenceSnapshot, error) {
	if service == nil || service.artifacts == nil || service.repository == nil || strings.TrimSpace(reviewer) == "" {
		return EvidenceSnapshot{}, ErrArtifactUnavailable
	}
	snapshot, reviews, _, err := service.load(ctx, reviewer)
	if err != nil {
		return EvidenceSnapshot{}, err
	}
	progress, err := buildProgress(snapshot, reviews)
	if err != nil {
		return EvidenceSnapshot{}, err
	}
	report := EvidenceSnapshot{
		SchemaVersion: EvidenceSchemaVersion, DatasetVersion: snapshot.DatasetVersion, CatalogSHA256: snapshot.CatalogSHA256, GovernanceSHA256: snapshot.Governance.ManifestSHA256,
		ReviewerScope: "current_authenticated_reviewer", Status: "human_review_in_progress",
		TotalCases: progress.Total, ReviewedCases: progress.Reviewed, ApprovedCases: progress.Approved,
		RejectedCases: progress.Rejected, PendingCases: progress.Pending, ReviewSetSHA256: progress.ReviewSetSHA256,
		ReadyForSealedMaterialization: progress.ReadyForMaterializing, Entries: make([]EvidenceEntry, 0, len(reviews)),
		Guardrails:  []string{"catalog_governance_and_case_hash_bound", "latest_append_only_revision_per_case", "reviewer_identity_not_exported", "no_prompt_or_expected_payload_exported", "self_hash_verified", "no_baseline_auto_freeze"},
		Limitations: []string{"该快照只证明当前登录复核人的逐例决策，不代表双人独立标注。", "ready 只允许进入独立封存与重跑评测，不代表正式基线或生产切流已获批。"},
	}
	if progress.Rejected > 0 {
		report.Status = "correction_required"
	}
	if progress.ReadyForMaterializing {
		report.Status = "ready_for_sealed_materialization"
	}
	for _, review := range reviews {
		view, viewErr := reviewView(review)
		if viewErr != nil {
			return EvidenceSnapshot{}, viewErr
		}
		entry := EvidenceEntry{CaseID: review.CaseID, Slice: review.Slice, CaseSHA256: review.CaseSHA256, Revision: view.Revision, Decision: view.Decision, ReasonCodes: view.ReasonCodes, ReviewSHA256: view.ReviewSHA256, ReviewedAt: view.ReviewedAt.UTC()}
		report.Entries = append(report.Entries, entry)
		if report.LastReviewedAt == nil || entry.ReviewedAt.After(*report.LastReviewedAt) {
			value := entry.ReviewedAt
			report.LastReviewedAt = &value
		}
	}
	sort.Slice(report.Entries, func(i, j int) bool { return report.Entries[i].CaseID < report.Entries[j].CaseID })
	if err := FinalizeEvidenceSnapshot(&report); err != nil {
		return EvidenceSnapshot{}, err
	}
	return report, nil
}

func FinalizeEvidenceSnapshot(report *EvidenceSnapshot) error {
	if report == nil {
		return ErrArtifactUnavailable
	}
	report.SnapshotSHA256 = ""
	if err := ValidateEvidenceSnapshot(*report, false); err != nil {
		return err
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return err
	}
	report.SnapshotSHA256 = digest(string(encoded))
	return ValidateEvidenceSnapshot(*report, true)
}

func ValidateEvidenceSnapshot(report EvidenceSnapshot, requireHash bool) error {
	if report.SchemaVersion != EvidenceSchemaVersion || strings.TrimSpace(report.DatasetVersion) == "" || len(report.CatalogSHA256) != 64 || len(report.GovernanceSHA256) != 64 || report.ReviewerScope != "current_authenticated_reviewer" || report.TotalCases < 1 || report.ReviewedCases < 0 || report.ApprovedCases < 0 || report.RejectedCases < 0 || report.PendingCases < 0 || report.ReviewedCases != report.ApprovedCases+report.RejectedCases || report.TotalCases != report.ReviewedCases+report.PendingCases || len(report.ReviewSetSHA256) != 64 || len(report.Entries) != report.ReviewedCases {
		return errors.New("catalog review evidence identity or counts are invalid")
	}
	expectedReady := report.ReviewedCases == report.TotalCases && report.ApprovedCases == report.TotalCases && report.RejectedCases == 0
	if report.ReadyForSealedMaterialization != expectedReady {
		return errors.New("catalog review evidence readiness is inconsistent")
	}
	expectedStatus := "human_review_in_progress"
	if report.RejectedCases > 0 {
		expectedStatus = "correction_required"
	}
	if expectedReady {
		expectedStatus = "ready_for_sealed_materialization"
	}
	if report.Status != expectedStatus || len(report.Guardrails) == 0 || len(report.Limitations) == 0 {
		return errors.New("catalog review evidence status or boundaries are invalid")
	}
	seen, latest := map[string]struct{}{}, (*time.Time)(nil)
	hashInputs := make([]string, 0, len(report.Entries))
	for index, entry := range report.Entries {
		if strings.TrimSpace(entry.CaseID) == "" || strings.TrimSpace(entry.Slice) == "" || len(entry.CaseSHA256) != 64 || entry.Revision < 1 || (entry.Decision != "approved" && entry.Decision != "rejected") || len(entry.ReviewSHA256) != 64 || entry.ReviewedAt.IsZero() {
			return errors.New("catalog review evidence entry is invalid")
		}
		if _, duplicate := seen[entry.CaseID]; duplicate {
			return errors.New("catalog review evidence contains duplicate cases")
		}
		if index > 0 && report.Entries[index-1].CaseID >= entry.CaseID {
			return errors.New("catalog review evidence entries are not canonical")
		}
		seen[entry.CaseID] = struct{}{}
		normalizedReasons, err := normalizeReasons(entry.Decision, entry.ReasonCodes)
		if err != nil || len(normalizedReasons) != len(entry.ReasonCodes) {
			return errors.New("catalog review evidence contains invalid reasons")
		}
		for reasonIndex := range normalizedReasons {
			if normalizedReasons[reasonIndex] != entry.ReasonCodes[reasonIndex] {
				return errors.New("catalog review evidence reasons are not canonical")
			}
		}
		hashInputs = append(hashInputs, entry.CaseID+"\x00"+entry.ReviewSHA256)
		if latest == nil || entry.ReviewedAt.After(*latest) {
			value := entry.ReviewedAt.UTC()
			latest = &value
		}
	}
	sort.Strings(hashInputs)
	if digest(strings.Join(hashInputs, "\x00")) != report.ReviewSetSHA256 {
		return errors.New("catalog review evidence set hash is invalid")
	}
	if (latest == nil) != (report.LastReviewedAt == nil) || (latest != nil && !latest.Equal(report.LastReviewedAt.UTC())) {
		return errors.New("catalog review evidence latest review time is invalid")
	}
	if requireHash {
		supplied := report.SnapshotSHA256
		report.SnapshotSHA256 = ""
		encoded, _ := json.Marshal(report)
		if len(supplied) != 64 || digest(string(encoded)) != supplied {
			return errors.New("catalog review evidence snapshot hash is invalid")
		}
	}
	return nil
}

func ValidateEvidenceAgainstSnapshot(report EvidenceSnapshot, snapshot Snapshot) error {
	if err := ValidateEvidenceSnapshot(report, true); err != nil {
		return err
	}
	if report.DatasetVersion != snapshot.DatasetVersion || report.CatalogSHA256 != snapshot.CatalogSHA256 || report.GovernanceSHA256 != snapshot.Governance.ManifestSHA256 || report.TotalCases != len(snapshot.Cases) {
		return errors.New("catalog review evidence does not match the catalog snapshot")
	}
	caseByID := make(map[string]Case, len(snapshot.Cases))
	for _, item := range snapshot.Cases {
		caseByID[item.ID] = item
	}
	for _, entry := range report.Entries {
		item, exists := caseByID[entry.CaseID]
		if !exists || item.Slice != entry.Slice || item.CaseSHA256 != entry.CaseSHA256 {
			return errors.New("catalog review evidence contains a stale or unknown case")
		}
	}
	return nil
}

func RenderEvidenceMarkdown(report EvidenceSnapshot) (string, error) {
	if err := ValidateEvidenceSnapshot(report, true); err != nil {
		return "", err
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Full 320 人工复核增量证据\n\n")
	fmt.Fprintf(&builder, "- 状态：`%s`\n- 数据集：`%s`\n- Catalog SHA-256：`%s`\n- Governance SHA-256：`%s`\n- Review Set SHA-256：`%s`\n- Snapshot SHA-256：`%s`\n", report.Status, report.DatasetVersion, report.CatalogSHA256, report.GovernanceSHA256, report.ReviewSetSHA256, report.SnapshotSHA256)
	fmt.Fprintf(&builder, "- 进度：`%d/%d`；通过 `%d`；退回 `%d`；待复核 `%d`\n- 可进入独立封存：`%t`\n\n", report.ReviewedCases, report.TotalCases, report.ApprovedCases, report.RejectedCases, report.PendingCases, report.ReadyForSealedMaterialization)
	builder.WriteString("## 最新逐例决策\n\n")
	if len(report.Entries) == 0 {
		builder.WriteString("当前登录复核人尚未提交逐例决策。\n\n")
	} else {
		builder.WriteString("| Case | Slice | Decision | Revision | Case SHA | Review SHA |\n|---|---|---|---:|---|---|\n")
		for _, entry := range report.Entries {
			fmt.Fprintf(&builder, "| `%s` | `%s` | `%s` | %d | `%s` | `%s` |\n", entry.CaseID, entry.Slice, entry.Decision, entry.Revision, entry.CaseSHA256, entry.ReviewSHA256)
		}
		builder.WriteString("\n")
	}
	builder.WriteString("## 边界\n\n")
	for _, limitation := range report.Limitations {
		fmt.Fprintf(&builder, "- %s\n", limitation)
	}
	return builder.String(), nil
}
