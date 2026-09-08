package humanreviewimport

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"GopherAI/internal/catalogreview"
	"GopherAI/internal/evaluation"
	"GopherAI/internal/judgecalibration"
)

const ImportSchemaVersion = "human-review-conclusions-import-v1"

type CatalogService interface {
	List(context.Context, string, catalogreview.Query) (catalogreview.Workbench, error)
	Submit(context.Context, string, catalogreview.ReviewCommand) (catalogreview.Receipt, error)
}

type JudgeService interface {
	Audit(context.Context, string) (judgecalibration.Audit, error)
	SubmitWithComment(context.Context, string, string, evaluation.JudgeScores, string) (judgecalibration.ReviewReceipt, error)
}

type ImportReport struct {
	SchemaVersion    string                          `json:"schema_version"`
	SourceSHA256     string                          `json:"source_sha256"`
	WorkbookSHA256   string                          `json:"workbook_sha256,omitempty"`
	ReviewerScope    string                          `json:"reviewer_scope"`
	CatalogSHA256    string                          `json:"catalog_sha256"`
	GovernanceSHA256 string                          `json:"governance_sha256"`
	FullCreated      int                             `json:"full_created"`
	FullUnchanged    int                             `json:"full_unchanged"`
	JudgeCreated     int                             `json:"judge_created"`
	JudgeUnchanged   int                             `json:"judge_unchanged"`
	FullProgress     catalogreview.Progress          `json:"full_progress"`
	JudgeAgreement   evaluation.CalibrationAgreement `json:"judge_agreement"`
	Guardrails       []string                        `json:"guardrails"`
}

type ImportOptions struct {
	// ApprovedOnly carries forward only explicitly approved human conclusions.
	// Rejected rows stay pending in the new catalog lineage after their contract
	// or evidence has been corrected, so a previous rejection can never be
	// converted into approval by an automated migration.
	ApprovedOnly bool
	// ApprovedCases is the immutable source-line commitment for carry-forward.
	// It must contain every approved ordinal and must match the revised catalog
	// byte-for-byte. This prevents a human approval from silently following a
	// changed question or expected result into a new lineage.
	ApprovedCases map[int]ApprovedCaseCommitment
}

type ApprovedCaseCommitment struct {
	Ordinal    int    `json:"ordinal"`
	CaseID     string `json:"case_id"`
	CaseSHA256 string `json:"case_sha256"`
}

func Import(ctx context.Context, reviewer, workbookSHA string, conclusions Conclusions, catalog CatalogService, judge JudgeService) (ImportReport, error) {
	return ImportWithOptions(ctx, reviewer, workbookSHA, conclusions, catalog, judge, ImportOptions{})
}

func ImportWithOptions(ctx context.Context, reviewer, workbookSHA string, conclusions Conclusions, catalog CatalogService, judge JudgeService, options ImportOptions) (ImportReport, error) {
	reviewer, workbookSHA = strings.TrimSpace(reviewer), strings.ToLower(strings.TrimSpace(workbookSHA))
	if reviewer == "" || len(conclusions.SourceSHA256) != 64 || len(conclusions.Full) != FullCaseCount || len(conclusions.Judge) != JudgeCaseCount || catalog == nil || judge == nil {
		return ImportReport{}, ErrInvalidConclusions
	}
	if workbookSHA != "" && !isSHA256(workbookSHA) {
		return ImportReport{}, fmt.Errorf("%w: workbook sha256", ErrInvalidConclusions)
	}
	cases, workbench, err := loadCatalogCases(ctx, reviewer, catalog)
	if err != nil {
		return ImportReport{}, err
	}
	if len(cases) != FullCaseCount {
		return ImportReport{}, errors.New("catalog does not contain exactly 320 cases")
	}
	report := ImportReport{
		SchemaVersion: ImportSchemaVersion, SourceSHA256: conclusions.SourceSHA256, WorkbookSHA256: workbookSHA,
		ReviewerScope: "current_authenticated_reviewer_hash", CatalogSHA256: workbench.CatalogSHA256, GovernanceSHA256: workbench.GovernanceSHA256,
		Guardrails: []string{"exact_320_plus_30_shape", "catalog_and_governance_hash_bound", "append_only_revisions", "source_comments_preserved", "rejected_cases_block_sealing", "idempotent_resume"},
	}
	for index, desired := range conclusions.Full {
		if options.ApprovedOnly && desired.Decision != "approved" {
			continue
		}
		current := cases[index]
		if options.ApprovedOnly {
			commitment, exists := options.ApprovedCases[desired.Ordinal]
			if !exists || commitment.Ordinal != desired.Ordinal || commitment.CaseID != current.ID || commitment.CaseSHA256 != current.CaseSHA256 {
				return report, fmt.Errorf("approved Full row %03d changed since human review; carry-forward refused", desired.Ordinal)
			}
		}
		if current.Review != nil && current.Review.Decision == desired.Decision && equalStrings(current.Review.ReasonCodes, desired.ReasonCodes) && current.Review.Comment == desired.Comment {
			report.FullUnchanged++
			continue
		}
		expectedRevision := 0
		if current.Review != nil {
			expectedRevision = current.Review.Revision
		}
		receipt, submitErr := catalog.Submit(ctx, reviewer, catalogreview.ReviewCommand{
			CatalogSHA256: workbench.CatalogSHA256, GovernanceSHA256: workbench.GovernanceSHA256,
			CaseID: current.ID, CaseSHA256: current.CaseSHA256, ExpectedRevision: expectedRevision,
			Decision: desired.Decision, ReasonCodes: desired.ReasonCodes, Comment: desired.Comment,
			IdempotencyKey: fmt.Sprintf("human-review-%s-full-%03d", conclusions.SourceSHA256[:16], desired.Ordinal),
			Acknowledgment: catalogreview.Acknowledgment,
		})
		if submitErr != nil {
			return report, fmt.Errorf("import Full row %03d (%s): %w", desired.Ordinal, current.ID, submitErr)
		}
		if receipt.Created {
			report.FullCreated++
		} else {
			report.FullUnchanged++
		}
	}

	if options.ApprovedOnly {
		finalFull, finalErr := catalog.List(ctx, reviewer, catalogreview.Query{Status: "all", Page: 1, PageSize: 1})
		if finalErr != nil {
			return report, finalErr
		}
		report.FullProgress = finalFull.Progress
		report.Guardrails = append(report.Guardrails, "rejected_rows_remain_pending_after_catalog_revision")
		return report, nil
	}
	audit, err := judge.Audit(ctx, reviewer)
	if err != nil {
		return report, err
	}
	if len(audit.Cases) != JudgeCaseCount {
		return report, errors.New("Judge calibration does not contain exactly 30 cases")
	}
	for index, desired := range conclusions.Judge {
		current := audit.Cases[index]
		if current.HumanScores != nil && equalScores(*current.HumanScores, desired.Scores) && current.HumanComment == desired.Comment {
			report.JudgeUnchanged++
			continue
		}
		receipt, submitErr := judge.SubmitWithComment(ctx, reviewer, current.ID, desired.Scores, desired.Comment)
		if submitErr != nil {
			return report, fmt.Errorf("import Judge row %03d (%s): %w", desired.Ordinal, current.ID, submitErr)
		}
		if receipt.Created {
			report.JudgeCreated++
		} else {
			report.JudgeUnchanged++
		}
	}
	finalFull, err := catalog.List(ctx, reviewer, catalogreview.Query{Status: "all", Page: 1, PageSize: 1})
	if err != nil {
		return report, err
	}
	finalJudge, err := judge.Audit(ctx, reviewer)
	if err != nil {
		return report, err
	}
	report.FullProgress, report.JudgeAgreement = finalFull.Progress, finalJudge.Agreement
	return report, nil
}

func loadCatalogCases(ctx context.Context, reviewer string, catalog CatalogService) ([]catalogreview.CaseView, catalogreview.Workbench, error) {
	result := make([]catalogreview.CaseView, 0, FullCaseCount)
	var first catalogreview.Workbench
	for page := 1; ; page++ {
		workbench, err := catalog.List(ctx, reviewer, catalogreview.Query{Status: "all", Page: page, PageSize: 20})
		if err != nil {
			return nil, catalogreview.Workbench{}, err
		}
		if page == 1 {
			first = workbench
		} else if workbench.CatalogSHA256 != first.CatalogSHA256 || workbench.GovernanceSHA256 != first.GovernanceSHA256 {
			return nil, catalogreview.Workbench{}, catalogreview.ErrRevisionConflict
		}
		result = append(result, workbench.Cases...)
		if len(result) >= workbench.FilteredTotal {
			break
		}
		if len(workbench.Cases) == 0 || page > 16 {
			return nil, catalogreview.Workbench{}, errors.New("catalog pagination did not converge")
		}
	}
	return result, first, nil
}

func equalStrings(left, right []string) bool {
	leftCopy, rightCopy := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	if len(leftCopy) != len(rightCopy) {
		return false
	}
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}

func equalScores(left, right evaluation.JudgeScores) bool {
	return left.Relevance == right.Relevance && left.Completeness == right.Completeness && left.Helpfulness == right.Helpfulness && left.Groundedness == right.Groundedness && left.Safety == right.Safety
}

func isSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}
