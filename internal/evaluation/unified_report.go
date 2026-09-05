package evaluation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

const (
	UnifiedEvaluationSchemaVersion = "unified-evaluation-report-v1"
	UnifiedEvaluationRunnerVersion = "unified-eval-runner-v1"
)

type EvaluationArtifact struct {
	Name           string `json:"name"`
	DatasetVersion string `json:"dataset_version"`
	SHA256         string `json:"sha256"`
	CaseCount      int    `json:"case_count"`
}

type EvaluationCoverage struct {
	CatalogCases          int     `json:"catalog_cases"`
	CatalogValidatedCases int     `json:"catalog_validated_cases"`
	ExecutableCases       int     `json:"executable_cases"`
	CompletedCases        int     `json:"completed_cases"`
	CatalogOnlyCases      int     `json:"catalog_only_cases"`
	ExecutionCoverage     float64 `json:"execution_coverage"`
	CompletionRate        float64 `json:"completion_rate"`
}

type EvaluationFailureCluster struct {
	Slice   string   `json:"slice"`
	Code    string   `json:"code"`
	Count   int      `json:"count"`
	CaseIDs []string `json:"case_ids,omitempty"`
}

type EvaluationDecision struct {
	Status                 string   `json:"status"`
	TechnicalGatesPassed   bool     `json:"technical_gates_passed"`
	HumanReviewed          bool     `json:"human_reviewed"`
	BaselineEligible       bool     `json:"baseline_eligible"`
	DefaultTrafficEligible bool     `json:"default_traffic_eligible"`
	Blockers               []string `json:"blockers"`
}

type UnifiedEvaluationReport struct {
	SchemaVersion    string                     `json:"schema_version"`
	RunnerVersion    string                     `json:"runner_version"`
	RunID            string                     `json:"run_id"`
	CandidateVersion string                     `json:"candidate_version"`
	GeneratedAt      time.Time                  `json:"generated_at"`
	DatasetVersion   string                     `json:"dataset_version"`
	ManifestSHA256   string                     `json:"manifest_sha256"`
	Artifacts        []EvaluationArtifact       `json:"artifacts"`
	Coverage         EvaluationCoverage         `json:"coverage"`
	Scorecard        DeterministicScorecard     `json:"scorecard"`
	FailureClusters  []EvaluationFailureCluster `json:"failure_clusters"`
	Decision         EvaluationDecision         `json:"decision"`
	Limitations      []string                   `json:"limitations"`
}

type UnifiedEvaluationInput struct {
	CandidateVersion string
	GeneratedAt      time.Time
	Catalog          EvalCatalogValidationReport
	Artifacts        []EvaluationArtifact
	Intent           IntentCascadeReport
	RAG              RAGReport
	Diagnostic       DiagnosticEvaluationReport
	Tool             ToolEvaluationReport
	Memory           MemoryEvaluationReport
}

func BuildUnifiedEvaluationReport(input UnifiedEvaluationInput) (UnifiedEvaluationReport, error) {
	if strings.TrimSpace(input.CandidateVersion) == "" || input.GeneratedAt.IsZero() {
		return UnifiedEvaluationReport{}, errors.New("candidate version and generation time are required")
	}
	if !input.Catalog.Passed || input.Catalog.ActualTotal < 1 || len(input.Artifacts) != 5 {
		return UnifiedEvaluationReport{}, errors.New("a valid catalog and five executable source artifacts are required")
	}
	artifacts := append([]EvaluationArtifact(nil), input.Artifacts...)
	sort.Slice(artifacts, func(left, right int) bool { return artifacts[left].Name < artifacts[right].Name })
	if err := validateEvaluationArtifacts(artifacts); err != nil {
		return UnifiedEvaluationReport{}, err
	}
	scorecard, err := BuildDeterministicScorecard(input.CandidateVersion, input.GeneratedAt, input.Intent, input.RAG, input.Diagnostic, input.Tool, input.Memory)
	if err != nil {
		return UnifiedEvaluationReport{}, err
	}
	reviewedCatalogCases := 0
	for _, slice := range input.Catalog.Slices {
		reviewedCatalogCases += slice.ReviewCounts["human"]
	}
	humanReviewed := reviewedCatalogCases == input.Catalog.ActualTotal && input.Catalog.ActualTotal == input.Catalog.ExpectedTotal && scorecard.HumanReviewed
	baselineEligible := input.Catalog.Passed && scorecard.TechnicalGatesPassed && humanReviewed && scorecard.CompletionRate >= MinimumEvaluationCompletion
	blockers := []string{}
	if !scorecard.TechnicalGatesPassed {
		blockers = append(blockers, "technical_gate_failed")
	}
	if !humanReviewed {
		blockers = append(blockers, "human_review_pending")
	}
	status := "technical_candidate"
	if !scorecard.TechnicalGatesPassed {
		status = "rejected"
	} else if baselineEligible {
		status = "baseline_eligible"
	}
	report := UnifiedEvaluationReport{
		SchemaVersion: UnifiedEvaluationSchemaVersion, RunnerVersion: UnifiedEvaluationRunnerVersion,
		CandidateVersion: strings.TrimSpace(input.CandidateVersion), GeneratedAt: input.GeneratedAt.UTC(),
		DatasetVersion: input.Catalog.DatasetVersion, ManifestSHA256: input.Catalog.ManifestSHA256,
		Artifacts: artifacts, Scorecard: scorecard,
		Coverage: EvaluationCoverage{
			CatalogCases: input.Catalog.ExpectedTotal, CatalogValidatedCases: input.Catalog.ActualTotal,
			ExecutableCases: scorecard.CaseCount, CompletedCases: scorecard.CompletedCases,
			CatalogOnlyCases:  input.Catalog.ActualTotal - scorecard.CaseCount,
			ExecutionCoverage: safeFloatRatio(float64(scorecard.CaseCount), float64(input.Catalog.ActualTotal)),
			CompletionRate:    scorecard.CompletionRate,
		},
		FailureClusters: collectEvaluationFailureClusters(input),
		Decision: EvaluationDecision{
			Status: status, TechnicalGatesPassed: scorecard.TechnicalGatesPassed, HumanReviewed: humanReviewed,
			BaselineEligible: baselineEligible, DefaultTrafficEligible: false, Blockers: blockers,
		},
		Limitations: []string{
			"Full 320 目录中的 20 条 evidence-insufficient 安全补充集当前只做 Schema、Hash 与复核状态校验，不计入 300 条确定性 Scorecard。",
			"pending_user 标签未经人工复核，当前报告只能称为技术候选，不能冻结为正式基线或触发默认流量切换。",
			"输入报告来自不同时间的可复现评测运行；外部模型结果仍可能随供应商版本变化。",
		},
	}
	report.RunID = unifiedRunID(report)
	return report, nil
}

func ValidateUnifiedEvaluationReport(report UnifiedEvaluationReport) error {
	if report.SchemaVersion != UnifiedEvaluationSchemaVersion || report.RunnerVersion != UnifiedEvaluationRunnerVersion || !strings.HasPrefix(report.RunID, "evalrun-") || report.CandidateVersion == "" || report.GeneratedAt.IsZero() {
		return errors.New("unified evaluation report identity is invalid")
	}
	if len(report.ManifestSHA256) != 64 || report.Coverage.CatalogCases < 1 || report.Coverage.CatalogValidatedCases != report.Coverage.CatalogCases || report.Coverage.ExecutableCases != report.Scorecard.CaseCount || report.Coverage.CompletedCases != report.Scorecard.CompletedCases {
		return errors.New("unified evaluation report coverage is inconsistent")
	}
	if len(report.Artifacts) != 5 {
		return errors.New("unified evaluation report must contain five source artifacts")
	}
	if err := validateEvaluationArtifacts(report.Artifacts); err != nil {
		return err
	}
	if report.Decision.BaselineEligible && (!report.Decision.TechnicalGatesPassed || !report.Decision.HumanReviewed) {
		return errors.New("unified evaluation baseline decision is inconsistent")
	}
	if report.Decision.DefaultTrafficEligible {
		return errors.New("unified evaluation report cannot enable default traffic")
	}
	return nil
}

func NewEvaluationArtifact(name string, datasetVersion string, encoded []byte, caseCount int) EvaluationArtifact {
	digest := sha256.Sum256(encoded)
	return EvaluationArtifact{Name: strings.TrimSpace(name), DatasetVersion: strings.TrimSpace(datasetVersion), SHA256: hex.EncodeToString(digest[:]), CaseCount: caseCount}
}

func WriteUnifiedEvaluationMarkdown(writer io.Writer, report UnifiedEvaluationReport) error {
	if writer == nil {
		return errors.New("markdown writer is required")
	}
	status := map[string]string{"technical_candidate": "技术候选", "rejected": "技术门拒绝", "baseline_eligible": "可冻结基线"}[report.Decision.Status]
	if status == "" {
		status = report.Decision.Status
	}
	_, err := fmt.Fprintf(writer, `# GopherAI Unified Evaluation Report

- Run: %s
- Candidate: %s
- Generated: %s
- Decision: %s
- Technical gates: %t
- Human reviewed: %t
- Baseline eligible: %t

## Coverage

| Catalog | Validated | Executable | Completed | Catalog-only | Execution coverage |
|---:|---:|---:|---:|---:|---:|
| %d | %d | %d | %d | %d | %.2f%% |

## Slice scorecard

| Slice | Cases | Completion | Technical gate | Human reviewed | Passed |
|---|---:|---:|---:|---:|---:|
`, report.RunID, report.CandidateVersion, report.GeneratedAt.Format(time.RFC3339), status,
		report.Decision.TechnicalGatesPassed, report.Decision.HumanReviewed, report.Decision.BaselineEligible,
		report.Coverage.CatalogCases, report.Coverage.CatalogValidatedCases, report.Coverage.ExecutableCases,
		report.Coverage.CompletedCases, report.Coverage.CatalogOnlyCases, report.Coverage.ExecutionCoverage*100)
	if err != nil {
		return err
	}
	for _, slice := range report.Scorecard.Slices {
		if _, err := fmt.Fprintf(writer, "| %s | %d | %.2f%% | %t | %t | %t |\n", slice.Name, slice.CaseCount, slice.CompletionRate*100, slice.SourceTechnicalGate, slice.HumanReviewed, slice.Passed); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(writer, "\n## Observed failure clusters\n\n"); err != nil {
		return err
	}
	if len(report.FailureClusters) == 0 {
		_, err = io.WriteString(writer, "No deterministic case failures were observed.\n")
		return err
	}
	for _, cluster := range report.FailureClusters {
		if _, err := fmt.Fprintf(writer, "- `%s/%s`: %d case(s)\n", cluster.Slice, cluster.Code, cluster.Count); err != nil {
			return err
		}
	}
	return nil
}

func validateEvaluationArtifacts(artifacts []EvaluationArtifact) error {
	seen := map[string]struct{}{}
	for _, artifact := range artifacts {
		if artifact.Name == "" || artifact.DatasetVersion == "" || artifact.CaseCount < 1 || len(artifact.SHA256) != 64 {
			return fmt.Errorf("evaluation artifact %q is incomplete", artifact.Name)
		}
		if _, err := hex.DecodeString(artifact.SHA256); err != nil {
			return fmt.Errorf("evaluation artifact %s has invalid sha256", artifact.Name)
		}
		if _, duplicate := seen[artifact.Name]; duplicate {
			return fmt.Errorf("duplicate evaluation artifact %s", artifact.Name)
		}
		seen[artifact.Name] = struct{}{}
	}
	return nil
}

func collectEvaluationFailureClusters(input UnifiedEvaluationInput) []EvaluationFailureCluster {
	clusters := map[string]*EvaluationFailureCluster{}
	add := func(slice string, code string, id string) {
		key := slice + "/" + code
		cluster := clusters[key]
		if cluster == nil {
			cluster = &EvaluationFailureCluster{Slice: slice, Code: code}
			clusters[key] = cluster
		}
		cluster.Count++
		if strings.TrimSpace(id) != "" && len(cluster.CaseIDs) < 20 {
			cluster.CaseIDs = append(cluster.CaseIDs, id)
		}
	}
	for _, item := range input.Intent.Failures {
		code := "misclassification"
		if item.SevereMisroute {
			code = "severe_misroute"
		}
		add("intent", code, item.ID)
	}
	for _, item := range input.RAG.Cases {
		if item.Error != "" {
			add("rag", "runtime_error", item.ID)
		}
		if item.UnauthorizedHits > 0 {
			add("rag", "unauthorized_recall", item.ID)
		}
		if item.ExpectedToResolve && item.RecallAt5 < 1 {
			add("rag", "retrieval_miss", item.ID)
		}
		if item.ExpectedToResolve && !item.CitationCovered {
			add("rag", "citation_gap", item.ID)
		}
		if !item.ExpectedToResolve && item.AnswerResolved {
			add("rag", "unsupported_answer", item.ID)
		}
		if !item.ExpectedToResolve && !item.SafeRejection {
			add("rag", "unsafe_no_evidence", item.ID)
		}
	}
	for _, item := range input.Diagnostic.Cases {
		if item.Error != "" {
			add("diagnosis", "runtime_error", item.ID)
		}
		if !item.RootCauseHit {
			add("diagnosis", "root_cause_miss", item.ID)
		}
		if !item.VerificationCorrect {
			add("diagnosis", "verification_gap", item.ID)
		}
		if item.PrematureCertainty {
			add("diagnosis", "premature_certainty", item.ID)
		}
		if !item.ReadOnly {
			add("diagnosis", "unsafe_action", item.ID)
		}
	}
	for _, item := range input.Tool.Cases {
		if !item.Passed {
			add("tool", "case_failed_"+item.Category, item.ID)
		}
		if !item.Deterministic {
			add("tool", "nondeterministic_replay", item.ID)
		}
	}
	for _, item := range input.Memory.Cases {
		if len(item.ForbiddenHits) > 0 {
			add("memory", "forbidden_memory_injection", item.ID)
		}
		if !item.WithinBudget {
			add("memory", "context_budget_exceeded", item.ID)
		}
		if !item.Deterministic {
			add("memory", "nondeterministic_replay", item.ID)
		}
		for _, expected := range item.ExpectedKeys {
			if !contains(item.ActualKeys, expected) {
				add("memory", "relevant_memory_miss", item.ID)
				break
			}
		}
	}
	result := make([]EvaluationFailureCluster, 0, len(clusters))
	for _, cluster := range clusters {
		sort.Strings(cluster.CaseIDs)
		result = append(result, *cluster)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Slice == result[right].Slice {
			return result[left].Code < result[right].Code
		}
		return result[left].Slice < result[right].Slice
	})
	return result
}

func unifiedRunID(report UnifiedEvaluationReport) string {
	identity := struct {
		Candidate string               `json:"candidate"`
		Manifest  string               `json:"manifest"`
		Artifacts []EvaluationArtifact `json:"artifacts"`
	}{report.CandidateVersion, report.ManifestSHA256, report.Artifacts}
	encoded, _ := json.Marshal(identity)
	digest := sha256.Sum256(encoded)
	return "evalrun-" + hex.EncodeToString(digest[:])[:16]
}
