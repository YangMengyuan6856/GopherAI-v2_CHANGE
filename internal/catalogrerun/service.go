package catalogrerun

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"GopherAI/internal/catalogseal"
)

const (
	PlanSchemaVersion   = "evaluation-catalog-rerun-plan-v1"
	ReportSchemaVersion = "evaluation-catalog-rerun-report-v1"
	Acknowledgment      = "I_CONFIRM_EXECUTE_SEALED_CATALOG_TECHNICAL_RERUN"
	DefaultOutputRoot   = "/root/GopherAI_Runtime/evaluation/catalog-sealed-reruns"
	MaxLogBytes         = 8 << 20
)

var (
	ErrInvalidPlan   = errors.New("sealed catalog rerun plan is invalid")
	ErrInvalidReport = errors.New("sealed catalog rerun report is invalid")
	ErrBusy          = errors.New("sealed catalog rerun is already active")
	ErrStepFailed    = errors.New("sealed catalog rerun step failed")
)

var fixedStepIDs = []string{"intent", "rag", "diagnosis", "tool", "memory", "unified"}

type ReleaseManifest struct {
	ReleaseID          string            `json:"release_id"`
	Branch             string            `json:"branch"`
	GitSHA             string            `json:"git_sha"`
	SourceDirty        bool              `json:"source_dirty"`
	BuiltAt            time.Time         `json:"built_at"`
	BuildStrategy      string            `json:"build_strategy"`
	Target             string            `json:"target"`
	GoVersion          string            `json:"go_version"`
	GoBuildFlags       []string          `json:"go_build_flags"`
	IncludedComponents []string          `json:"included_components"`
	ConfigIncluded     bool              `json:"config_included"`
	Migrations         []json.RawMessage `json:"migrations"`
	Rollback           string            `json:"rollback"`
}

type Step struct {
	ID               string   `json:"id"`
	Binary           string   `json:"binary"`
	BinarySHA256     string   `json:"binary_sha256"`
	Arguments        []string `json:"arguments"`
	TimeoutSeconds   int      `json:"timeout_seconds"`
	AllowedExitCodes []int    `json:"allowed_exit_codes"`
	Outputs          []string `json:"outputs"`
}

type Plan struct {
	SchemaVersion         string   `json:"schema_version"`
	PlanSHA256            string   `json:"plan_sha256"`
	SealID                string   `json:"seal_id"`
	SealSHA256            string   `json:"seal_sha256"`
	ArtifactRoot          string   `json:"artifact_root"`
	OutputRoot            string   `json:"output_root"`
	WorkingDirectory      string   `json:"working_directory"`
	ReleaseManifestPath   string   `json:"release_manifest_path"`
	ReleaseManifestSHA256 string   `json:"release_manifest_sha256"`
	ReleaseID             string   `json:"release_id"`
	GitSHA                string   `json:"git_sha"`
	Steps                 []Step   `json:"steps"`
	Guardrails            []string `json:"guardrails"`
}

type Output struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type StepResult struct {
	ID              string    `json:"id"`
	Status          string    `json:"status"`
	ExitCode        int       `json:"exit_code"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	DurationMillis  int64     `json:"duration_millis"`
	StdoutSHA256    string    `json:"stdout_sha256"`
	StdoutBytes     int64     `json:"stdout_bytes"`
	StdoutTruncated bool      `json:"stdout_truncated"`
	StderrSHA256    string    `json:"stderr_sha256"`
	StderrBytes     int64     `json:"stderr_bytes"`
	StderrTruncated bool      `json:"stderr_truncated"`
	Outputs         []Output  `json:"outputs"`
}

type Report struct {
	SchemaVersion       string       `json:"schema_version"`
	ReportSHA256        string       `json:"report_sha256"`
	RunID               string       `json:"run_id"`
	PlanSHA256          string       `json:"plan_sha256"`
	SealID              string       `json:"seal_id"`
	SealSHA256          string       `json:"seal_sha256"`
	ReleaseID           string       `json:"release_id"`
	GitSHA              string       `json:"git_sha"`
	Status              string       `json:"status"`
	Checkpoint          int          `json:"checkpoint"`
	StartedAt           time.Time    `json:"started_at"`
	FinishedAt          *time.Time   `json:"finished_at,omitempty"`
	TechnicalGatePassed bool         `json:"technical_gate_passed"`
	PromotionEligible   bool         `json:"promotion_eligible"`
	FailureStep         string       `json:"failure_step,omitempty"`
	Steps               []StepResult `json:"steps"`
	NextRequiredGate    string       `json:"next_required_gate"`
	Guardrails          []string     `json:"guardrails"`
}

type ExecutionResult struct {
	ExitCode int
	Err      error
}

type Executor interface {
	Run(context.Context, string, []string, string, io.Writer, io.Writer) ExecutionResult
}

type OSExecutor struct{}

func (OSExecutor) Run(ctx context.Context, binary string, arguments []string, workingDirectory string, stdout, stderr io.Writer) ExecutionResult {
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Dir = workingDirectory
	command.Env = append(os.Environ(), "GOMAXPROCS=1")
	command.Stdin, command.Stdout, command.Stderr = nil, stdout, stderr
	err := command.Run()
	if err == nil {
		return ExecutionResult{ExitCode: 0}
	}
	if ctx.Err() != nil {
		return ExecutionResult{ExitCode: 124, Err: ctx.Err()}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return ExecutionResult{ExitCode: exitErr.ExitCode(), Err: err}
	}
	return ExecutionResult{ExitCode: 125, Err: err}
}

func BuildPlan(artifactRoot, outputRoot, workingDirectory, releaseManifestPath string) (Plan, error) {
	artifactRoot, err := cleanAbsolute(artifactRoot)
	if err != nil {
		return Plan{}, err
	}
	outputRoot, err = cleanAbsolute(outputRoot)
	if err != nil {
		return Plan{}, err
	}
	workingDirectory, err = cleanAbsolute(workingDirectory)
	if err != nil {
		return Plan{}, err
	}
	releaseManifestPath, err = cleanAbsolute(releaseManifestPath)
	if err != nil || releaseManifestPath != filepath.Join(workingDirectory, "release-manifest.json") || pathsOverlap(artifactRoot, outputRoot) || pathsOverlap(artifactRoot, workingDirectory) || pathsOverlap(outputRoot, workingDirectory) {
		return Plan{}, ErrInvalidPlan
	}
	seal, err := catalogseal.ValidateArtifact(artifactRoot)
	if err != nil {
		return Plan{}, ErrInvalidPlan
	}
	release, releaseSHA, err := loadReleaseManifest(releaseManifestPath)
	if err != nil || !releaseEligible(release) {
		return Plan{}, ErrInvalidPlan
	}
	if !hasComponents(release.IncludedComponents, requiredComponents()) {
		return Plan{}, ErrInvalidPlan
	}
	steps, err := buildSteps(artifactRoot, workingDirectory, seal.SealID, release.ReleaseID)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		SchemaVersion: PlanSchemaVersion, SealID: seal.SealID, SealSHA256: seal.SealSHA256,
		ArtifactRoot: artifactRoot, OutputRoot: outputRoot, WorkingDirectory: workingDirectory,
		ReleaseManifestPath: releaseManifestPath, ReleaseManifestSHA256: releaseSHA,
		ReleaseID: release.ReleaseID, GitSHA: release.GitSHA, Steps: steps,
		Guardrails: []string{"validated_seal_only", "fixed_binary_and_argument_registry", "binary_hash_bound", "single_active_run_per_seal", "per_step_timeout", "append_only_checkpoints", "no_shell", "no_baseline_pointer_write", "no_policy_write", "no_automatic_promotion"},
	}
	if err := finalizePlan(&plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func ValidatePlan(plan Plan) error {
	if err := validatePlanIntegrity(plan); err != nil {
		return ErrInvalidPlan
	}
	seal, err := catalogseal.ValidateArtifact(plan.ArtifactRoot)
	if err != nil || seal.SealID != plan.SealID || seal.SealSHA256 != plan.SealSHA256 {
		return ErrInvalidPlan
	}
	release, releaseSHA, err := loadReleaseManifest(plan.ReleaseManifestPath)
	if err != nil || !releaseEligible(release) || releaseSHA != plan.ReleaseManifestSHA256 || release.ReleaseID != plan.ReleaseID || release.GitSHA != plan.GitSHA || !hasComponents(release.IncludedComponents, requiredComponents()) {
		return ErrInvalidPlan
	}
	expectedSteps, err := buildSteps(plan.ArtifactRoot, plan.WorkingDirectory, plan.SealID, plan.ReleaseID)
	if err != nil || !reflect.DeepEqual(expectedSteps, plan.Steps) || plan.ReleaseManifestPath != filepath.Join(plan.WorkingDirectory, "release-manifest.json") || pathsOverlap(plan.ArtifactRoot, plan.OutputRoot) || pathsOverlap(plan.ArtifactRoot, plan.WorkingDirectory) || pathsOverlap(plan.OutputRoot, plan.WorkingDirectory) {
		return ErrInvalidPlan
	}
	return nil
}

func Execute(ctx context.Context, plan Plan, executor Executor, clock func() time.Time) (Report, string, error) {
	if executor == nil || ctx == nil || ValidatePlan(plan) != nil {
		return Report{}, "", ErrInvalidPlan
	}
	if clock == nil {
		clock = time.Now
	}
	base := filepath.Join(plan.OutputRoot, plan.SealID)
	if err := os.MkdirAll(base, 0o750); err != nil {
		return Report{}, "", err
	}
	lockPath := filepath.Join(base, "active.lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return Report{}, "", ErrBusy
		}
		return Report{}, "", err
	}
	started := clock().UTC()
	runID := "rerun-" + started.Format("20060102T150405.000000000Z") + "-" + plan.PlanSHA256[:8]
	if _, err := lock.WriteString(runID + "\n"); err != nil {
		lock.Close()
		os.Remove(lockPath)
		return Report{}, "", err
	}
	if err := lock.Sync(); err != nil {
		lock.Close()
		os.Remove(lockPath)
		return Report{}, "", err
	}
	lock.Close()
	defer os.Remove(lockPath)
	runDirectory := filepath.Join(base, runID)
	if err := os.Mkdir(runDirectory, 0o750); err != nil {
		return Report{}, "", err
	}
	planBytes, _ := json.MarshalIndent(plan, "", "  ")
	if err := writeExclusive(filepath.Join(runDirectory, "plan.json"), append(planBytes, '\n'), 0o640); err != nil {
		return Report{}, runDirectory, err
	}
	report := Report{
		SchemaVersion: ReportSchemaVersion, RunID: runID, PlanSHA256: plan.PlanSHA256,
		SealID: plan.SealID, SealSHA256: plan.SealSHA256, ReleaseID: plan.ReleaseID, GitSHA: plan.GitSHA,
		Status: "running", StartedAt: started, PromotionEligible: false,
		NextRequiredGate: "完成固定六步技术重跑；任何失败都保留证据并阻断后续步骤。",
		Guardrails:       []string{"sealed_input_revalidated_before_execution", "runner_binary_hash_revalidated", "sequential_fixed_dag", "bounded_logs", "no_hidden_retry", "failed_run_preserved", "technical_pass_does_not_promote", "no_baseline_pointer_write", "no_policy_write"},
	}
	if err := saveCheckpoint(runDirectory, &report); err != nil {
		return Report{}, runDirectory, err
	}
	technicalGateFailed := false
	for _, step := range plan.Steps {
		result, stepErr := runStep(ctx, executor, plan, step, runDirectory, clock)
		report.Steps = append(report.Steps, result)
		if stepErr != nil {
			report.Status, report.FailureStep = "failed", step.ID
			report.NextRequiredGate = "检查失败步骤的 stderr 与已生成输出；修复环境或代码后创建新 Run，不得复用失败结果晋级。"
			return finishReport(runDirectory, report, clock, fmt.Errorf("%w: %s", ErrStepFailed, step.ID))
		}
		if result.Status == "technical_gate_failed" {
			technicalGateFailed = true
		}
		report.Checkpoint = len(report.Steps)
		if err := saveCheckpoint(runDirectory, &report); err != nil {
			return Report{}, runDirectory, err
		}
	}
	if technicalGateFailed {
		report.Status = "completed_technical_gate_failed"
		report.NextRequiredGate = "分析统一报告的失败簇并修正候选或运行环境；技术门未通过，禁止进入基线审批。"
	} else {
		report.Status, report.TechnicalGatePassed = "completed_technical_pass", true
		report.NextRequiredGate = "独立复验本 Run、完成 Judge 人工校准并提交基线审批；技术通过不会自动晋级。"
	}
	return finishReport(runDirectory, report, clock, nil)
}

func runStep(parent context.Context, executor Executor, plan Plan, step Step, runDirectory string, clock func() time.Time) (StepResult, error) {
	result := StepResult{ID: step.ID, Status: "running", StartedAt: clock().UTC()}
	stdoutPath := filepath.Join(runDirectory, step.ID+".stdout.log")
	stderrPath := filepath.Join(runDirectory, step.ID+".stderr.log")
	stdoutFile, err := os.OpenFile(stdoutPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return result, err
	}
	stderrFile, err := os.OpenFile(stderrPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		stdoutFile.Close()
		return result, err
	}
	stdout := &boundedWriter{writer: stdoutFile, remaining: MaxLogBytes}
	stderr := &boundedWriter{writer: stderrFile, remaining: MaxLogBytes}
	stepCtx, cancel := context.WithTimeout(parent, time.Duration(step.TimeoutSeconds)*time.Second)
	execution := executor.Run(stepCtx, step.Binary, resolveArguments(step.Arguments, runDirectory), plan.WorkingDirectory, stdout, stderr)
	cancel()
	stdoutSyncErr, stderrSyncErr := stdoutFile.Sync(), stderrFile.Sync()
	stdoutCloseErr, stderrCloseErr := stdoutFile.Close(), stderrFile.Close()
	result.FinishedAt = clock().UTC()
	result.DurationMillis = result.FinishedAt.Sub(result.StartedAt).Milliseconds()
	result.ExitCode, result.StdoutTruncated, result.StderrTruncated = execution.ExitCode, stdout.truncated, stderr.truncated
	result.StdoutSHA256, result.StdoutBytes, _ = hashFile(stdoutPath)
	result.StderrSHA256, result.StderrBytes, _ = hashFile(stderrPath)
	if stdoutSyncErr != nil || stderrSyncErr != nil || stdoutCloseErr != nil || stderrCloseErr != nil {
		result.Status = "failed"
		return result, ErrStepFailed
	}
	result.Outputs, err = collectOutputs(runDirectory, step.Outputs)
	allowed := exitAllowed(step.AllowedExitCodes, execution.ExitCode)
	if !allowed || err != nil {
		result.Status = "failed"
		return result, ErrStepFailed
	}
	if execution.ExitCode == 0 {
		result.Status = "passed"
	} else {
		result.Status = "technical_gate_failed"
	}
	return result, nil
}

func finishReport(runDirectory string, report Report, clock func() time.Time, runErr error) (Report, string, error) {
	finished := clock().UTC()
	report.FinishedAt, report.Checkpoint = &finished, len(report.Steps)+1
	if err := saveCheckpoint(runDirectory, &report); err != nil {
		return Report{}, runDirectory, err
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	if err := writeExclusive(filepath.Join(runDirectory, "result.json"), append(encoded, '\n'), 0o640); err != nil {
		return Report{}, runDirectory, err
	}
	if err := ValidateReport(report); err != nil {
		return Report{}, runDirectory, err
	}
	return report, runDirectory, runErr
}

func ValidateReport(report Report) error {
	if report.SchemaVersion != ReportSchemaVersion || len(report.ReportSHA256) != 64 || strings.TrimSpace(report.RunID) == "" || len(report.PlanSHA256) != 64 || len(report.SealID) != len("catalog-seal-")+32 || len(report.SealSHA256) != 64 || strings.TrimSpace(report.ReleaseID) == "" || len(report.GitSHA) != 40 || report.StartedAt.IsZero() || report.FinishedAt == nil || report.FinishedAt.Before(report.StartedAt) || report.Checkpoint != len(report.Steps)+1 || report.PromotionEligible || strings.TrimSpace(report.NextRequiredGate) == "" || len(report.Guardrails) == 0 {
		return ErrInvalidReport
	}
	switch report.Status {
	case "failed":
		if report.FailureStep == "" || report.TechnicalGatePassed || len(report.Steps) == 0 {
			return ErrInvalidReport
		}
	case "completed_technical_gate_failed":
		if report.FailureStep != "" || report.TechnicalGatePassed || len(report.Steps) != 6 {
			return ErrInvalidReport
		}
	case "completed_technical_pass":
		if report.FailureStep != "" || !report.TechnicalGatePassed || len(report.Steps) != 6 {
			return ErrInvalidReport
		}
	default:
		return ErrInvalidReport
	}
	if len(report.Steps) > len(fixedStepIDs) {
		return ErrInvalidReport
	}
	seenOutputs := map[string]struct{}{}
	hasGateFailure, hasExecutionFailure := false, false
	for index, step := range report.Steps {
		if step.ID != fixedStepIDs[index] || step.StartedAt.IsZero() || step.FinishedAt.Before(step.StartedAt) || step.DurationMillis < 0 || step.StdoutBytes < 0 || step.StderrBytes < 0 || len(step.StdoutSHA256) != 64 || len(step.StderrSHA256) != 64 {
			return ErrInvalidReport
		}
		switch step.Status {
		case "passed":
			if step.ExitCode != 0 || len(step.Outputs) == 0 {
				return ErrInvalidReport
			}
		case "technical_gate_failed":
			hasGateFailure = true
			if step.ExitCode == 0 || len(step.Outputs) == 0 {
				return ErrInvalidReport
			}
		case "failed":
			hasExecutionFailure = true
		default:
			return ErrInvalidReport
		}
		for _, output := range step.Outputs {
			if !safeRelativePath(output.Path) || len(output.SHA256) != 64 || output.Bytes <= 0 {
				return ErrInvalidReport
			}
			if _, duplicate := seenOutputs[output.Path]; duplicate {
				return ErrInvalidReport
			}
			seenOutputs[output.Path] = struct{}{}
		}
	}
	if (report.Status == "failed" && (!hasExecutionFailure || report.Steps[len(report.Steps)-1].ID != report.FailureStep || report.Steps[len(report.Steps)-1].Status != "failed")) || (report.Status == "completed_technical_gate_failed" && (!hasGateFailure || hasExecutionFailure)) || (report.Status == "completed_technical_pass" && (hasGateFailure || hasExecutionFailure)) {
		return ErrInvalidReport
	}
	expected := report.ReportSHA256
	copyReport := report
	if err := finalizeReport(&copyReport); err != nil || copyReport.ReportSHA256 != expected {
		return ErrInvalidReport
	}
	return nil
}

// ValidateRunDirectory independently re-hashes the physical evidence referenced by
// a completed rerun. It intentionally validates the stored plan without requiring
// the historical release directory to remain present.
func ValidateRunDirectory(runDirectory string) (Report, error) {
	runDirectory, err := cleanAbsolute(runDirectory)
	if err != nil {
		return Report{}, ErrInvalidReport
	}
	var report Report
	if err := readStrictJSON(filepath.Join(runDirectory, "result.json"), 4<<20, &report); err != nil || ValidateReport(report) != nil || filepath.Base(runDirectory) != report.RunID {
		return Report{}, ErrInvalidReport
	}
	var plan Plan
	if err := readStrictJSON(filepath.Join(runDirectory, "plan.json"), 2<<20, &plan); err != nil || validatePlanIntegrity(plan) != nil || !reportMatchesPlan(report, plan) {
		return Report{}, ErrInvalidReport
	}
	expected := map[string]struct{}{"plan.json": {}, "result.json": {}}
	for _, step := range report.Steps {
		stdoutName, stderrName := step.ID+".stdout.log", step.ID+".stderr.log"
		expected[stdoutName], expected[stderrName] = struct{}{}, struct{}{}
		if !fileMatches(filepath.Join(runDirectory, stdoutName), step.StdoutSHA256, step.StdoutBytes) || !fileMatches(filepath.Join(runDirectory, stderrName), step.StderrSHA256, step.StderrBytes) {
			return Report{}, ErrInvalidReport
		}
		for _, output := range step.Outputs {
			expected[output.Path] = struct{}{}
			if !fileMatches(filepath.Join(runDirectory, filepath.FromSlash(output.Path)), output.SHA256, output.Bytes) {
				return Report{}, ErrInvalidReport
			}
		}
	}
	for index, step := range plan.Steps[:len(report.Steps)] {
		for _, output := range step.Outputs {
			expected[output] = struct{}{}
		}
		if step.ID != report.Steps[index].ID {
			return Report{}, ErrInvalidReport
		}
	}
	checkpointIndexes := expectedCheckpointIndexes(report)
	for _, index := range checkpointIndexes {
		name := fmt.Sprintf("checkpoint-%02d.json", index)
		expected[name] = struct{}{}
		var checkpoint Report
		if err := readStrictJSON(filepath.Join(runDirectory, name), 4<<20, &checkpoint); err != nil || !validCheckpoint(checkpoint, report, index) {
			return Report{}, ErrInvalidReport
		}
	}
	entries, err := os.ReadDir(runDirectory)
	if err != nil {
		return Report{}, ErrInvalidReport
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return Report{}, ErrInvalidReport
		}
		if _, allowed := expected[entry.Name()]; !allowed {
			return Report{}, ErrInvalidReport
		}
	}
	return report, nil
}

func buildSteps(artifactRoot, binDirectory, sealID, releaseID string) ([]Step, error) {
	evalRoot := filepath.Join(artifactRoot, "evals")
	candidate := "sealed:" + sealID + ":" + releaseID
	definitions := []struct {
		id, binary string
		arguments  []string
		timeout    int
		allowed    []int
		outputs    []string
	}{
		{"intent", "GopherAI-intent-eval", []string{"-dataset", filepath.Join(evalRoot, "devsupport-intent-v1.jsonl"), "-json", "{{RUN_DIR}}/intent.json", "-markdown", "{{RUN_DIR}}/intent.md", "-candidate", candidate, "-mode", "cascade"}, 600, []int{0}, []string{"intent.json", "intent.md"}},
		{"rag", "GopherAI-rag-eval", []string{"-dataset", filepath.Join(evalRoot, "devsupport-rag-core-v2.jsonl"), "-fixture", filepath.Join(evalRoot, "fixtures", "kb-fixture-v2.json"), "-out-json", "{{RUN_DIR}}/rag.json", "-out-md", "{{RUN_DIR}}/rag.md", "-candidate", candidate}, 2700, []int{0, 1}, []string{"rag.json", "rag.md"}},
		{"diagnosis", "GopherAI-diagnostic-eval", []string{"-dataset", filepath.Join(evalRoot, "devsupport-diagnostic-v1.jsonl"), "-report", "{{RUN_DIR}}/diagnosis.json"}, 120, []int{0}, []string{"diagnosis.json"}},
		{"tool", "GopherAI-tool-eval", []string{"-dataset", filepath.Join(evalRoot, "devsupport-tool-runtime-v1.jsonl"), "-report", "{{RUN_DIR}}/tool.json"}, 120, []int{0}, []string{"tool.json"}},
		{"memory", "GopherAI-memory-eval", []string{"-dataset", filepath.Join(evalRoot, "devsupport-memory-v1.jsonl"), "-report", "{{RUN_DIR}}/memory.json"}, 120, []int{0}, []string{"memory.json"}},
		{"unified", "GopherAI-eval-runner", []string{"-manifest", filepath.Join(evalRoot, "devsupport-eval-v1.manifest.json"), "-review-manifest", filepath.Join(evalRoot, "devsupport-eval-v1.review.json"), "-intent", "{{RUN_DIR}}/intent.json", "-rag", "{{RUN_DIR}}/rag.json", "-diagnosis", "{{RUN_DIR}}/diagnosis.json", "-tool", "{{RUN_DIR}}/tool.json", "-memory", "{{RUN_DIR}}/memory.json", "-candidate", candidate, "-out-json", "{{RUN_DIR}}/unified.json", "-out-md", "{{RUN_DIR}}/unified.md"}, 120, []int{0, 2}, []string{"unified.json", "unified.md"}},
	}
	steps := make([]Step, 0, len(definitions))
	for _, definition := range definitions {
		binary := filepath.Join(binDirectory, definition.binary)
		hash, size, err := hashFile(binary)
		if err != nil || size <= 0 {
			return nil, ErrInvalidPlan
		}
		steps = append(steps, Step{ID: definition.id, Binary: binary, BinarySHA256: hash, Arguments: definition.arguments, TimeoutSeconds: definition.timeout, AllowedExitCodes: definition.allowed, Outputs: definition.outputs})
	}
	return steps, nil
}

func validatePlanShape(plan Plan) error {
	if plan.SchemaVersion != PlanSchemaVersion || len(plan.PlanSHA256) != 64 || len(plan.SealID) != len("catalog-seal-")+32 || len(plan.SealSHA256) != 64 || strings.TrimSpace(plan.ArtifactRoot) == "" || strings.TrimSpace(plan.OutputRoot) == "" || strings.TrimSpace(plan.WorkingDirectory) == "" || strings.TrimSpace(plan.ReleaseManifestPath) == "" || len(plan.ReleaseManifestSHA256) != 64 || strings.TrimSpace(plan.ReleaseID) == "" || len(plan.GitSHA) != 40 || len(plan.Steps) != 6 || len(plan.Guardrails) == 0 {
		return ErrInvalidPlan
	}
	seen := map[string]struct{}{}
	for _, step := range plan.Steps {
		if step.ID == "" || strings.TrimSpace(step.Binary) == "" || len(step.BinarySHA256) != 64 || len(step.Arguments) == 0 || step.TimeoutSeconds <= 0 || len(step.AllowedExitCodes) == 0 || len(step.Outputs) == 0 {
			return ErrInvalidPlan
		}
		if _, duplicate := seen[step.ID]; duplicate {
			return ErrInvalidPlan
		}
		seen[step.ID] = struct{}{}
		for _, output := range step.Outputs {
			if !safeRelativePath(output) {
				return ErrInvalidPlan
			}
		}
	}
	return nil
}

func finalizePlan(plan *Plan) error {
	if plan == nil {
		return ErrInvalidPlan
	}
	plan.PlanSHA256 = strings.Repeat("0", 64)
	if err := validatePlanShape(*plan); err != nil {
		return err
	}
	plan.PlanSHA256 = ""
	encoded, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	plan.PlanSHA256 = digest(encoded)
	return nil
}

func validatePlanIntegrity(plan Plan) error {
	if err := validatePlanShape(plan); err != nil {
		return ErrInvalidPlan
	}
	expected := plan.PlanSHA256
	copyPlan := plan
	if err := finalizePlan(&copyPlan); err != nil || copyPlan.PlanSHA256 != expected {
		return ErrInvalidPlan
	}
	return nil
}

func finalizeReport(report *Report) error {
	if report == nil {
		return ErrInvalidReport
	}
	report.ReportSHA256 = ""
	encoded, err := json.Marshal(report)
	if err != nil {
		return err
	}
	report.ReportSHA256 = digest(encoded)
	return nil
}

func reportMatchesPlan(report Report, plan Plan) bool {
	if report.PlanSHA256 != plan.PlanSHA256 || report.SealID != plan.SealID || report.SealSHA256 != plan.SealSHA256 || report.ReleaseID != plan.ReleaseID || report.GitSHA != plan.GitSHA || len(report.Steps) > len(plan.Steps) {
		return false
	}
	for index, result := range report.Steps {
		step := plan.Steps[index]
		if result.ID != step.ID {
			return false
		}
		allowedOutputs := make(map[string]struct{}, len(step.Outputs))
		for _, output := range step.Outputs {
			allowedOutputs[output] = struct{}{}
		}
		for _, output := range result.Outputs {
			if _, allowed := allowedOutputs[output.Path]; !allowed {
				return false
			}
		}
	}
	return true
}

func expectedCheckpointIndexes(report Report) []int {
	lastRunning := len(report.Steps)
	if report.Status == "failed" {
		lastRunning--
	}
	indexes := make([]int, 0, lastRunning+2)
	for index := 0; index <= lastRunning; index++ {
		indexes = append(indexes, index)
	}
	return append(indexes, report.Checkpoint)
}

func validCheckpoint(checkpoint, final Report, index int) bool {
	expectedHash := checkpoint.ReportSHA256
	copyCheckpoint := checkpoint
	if err := finalizeReport(&copyCheckpoint); err != nil || copyCheckpoint.ReportSHA256 != expectedHash {
		return false
	}
	if index == final.Checkpoint {
		return reflect.DeepEqual(checkpoint, final)
	}
	if checkpoint.SchemaVersion != final.SchemaVersion || checkpoint.RunID != final.RunID || checkpoint.PlanSHA256 != final.PlanSHA256 || checkpoint.SealID != final.SealID || checkpoint.SealSHA256 != final.SealSHA256 || checkpoint.ReleaseID != final.ReleaseID || checkpoint.GitSHA != final.GitSHA || checkpoint.StartedAt != final.StartedAt || checkpoint.Checkpoint != index || checkpoint.Status != "running" || checkpoint.FinishedAt != nil || checkpoint.TechnicalGatePassed || checkpoint.PromotionEligible || checkpoint.FailureStep != "" || checkpoint.NextRequiredGate == "" || !reflect.DeepEqual(checkpoint.Guardrails, final.Guardrails) || len(checkpoint.Steps) != index {
		return false
	}
	return index == 0 || reflect.DeepEqual(checkpoint.Steps, final.Steps[:index])
}

func readStrictJSON(filePath string, maximumBytes int64, destination any) error {
	info, err := os.Stat(filePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumBytes {
		return ErrInvalidReport
	}
	encoded, err := os.ReadFile(filePath)
	if err != nil {
		return ErrInvalidReport
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return ErrInvalidReport
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidReport
	}
	return nil
}

func fileMatches(filePath, expectedHash string, expectedBytes int64) bool {
	actualHash, actualBytes, err := hashFile(filePath)
	return err == nil && actualHash == expectedHash && actualBytes == expectedBytes
}

func safeRelativePath(value string) bool {
	return value != "" && !strings.Contains(value, "\\") && !path.IsAbs(value) && path.Clean(value) == value && value != "." && value != ".." && !strings.HasPrefix(value, "../")
}

func saveCheckpoint(runDirectory string, report *Report) error {
	if err := finalizeReport(report); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	name := fmt.Sprintf("checkpoint-%02d.json", report.Checkpoint)
	return writeExclusive(filepath.Join(runDirectory, name), append(encoded, '\n'), 0o640)
}

func collectOutputs(runDirectory string, names []string) ([]Output, error) {
	outputs := make([]Output, 0, len(names))
	for _, name := range names {
		path := filepath.Join(runDirectory, name)
		hash, size, err := hashFile(path)
		if err != nil || size <= 0 {
			return outputs, ErrStepFailed
		}
		if err := os.Chmod(path, 0o640); err != nil {
			return outputs, err
		}
		outputs = append(outputs, Output{Path: name, SHA256: hash, Bytes: size})
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Path < outputs[j].Path })
	return outputs, nil
}

func loadReleaseManifest(path string) (ReleaseManifest, string, error) {
	var manifest ReleaseManifest
	encoded, err := os.ReadFile(path)
	if err != nil || len(encoded) == 0 || len(encoded) > 64<<10 {
		return manifest, "", ErrInvalidPlan
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, "", ErrInvalidPlan
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return manifest, "", ErrInvalidPlan
	}
	return manifest, digest(encoded), nil
}

func releaseEligible(manifest ReleaseManifest) bool {
	return strings.TrimSpace(manifest.ReleaseID) != "" && len(manifest.GitSHA) == 40 && !manifest.SourceDirty && !manifest.BuiltAt.IsZero() && manifest.Target == "linux/amd64" && strings.TrimSpace(manifest.BuildStrategy) != "" && strings.TrimSpace(manifest.Rollback) != ""
}

func requiredComponents() []string {
	return []string{"catalog-sealed-candidate-verifier", "catalog-sealed-rerun", "diagnostic-eval", "intent-eval", "memory-eval", "rag-eval", "tool-eval", "unified-eval-runner"}
}

func hasComponents(actual, required []string) bool {
	set := make(map[string]struct{}, len(actual))
	for _, component := range actual {
		set[strings.TrimSpace(component)] = struct{}{}
	}
	for _, component := range required {
		if _, exists := set[component]; !exists {
			return false
		}
	}
	return true
}

func resolveArguments(arguments []string, runDirectory string) []string {
	result := make([]string, len(arguments))
	for index, argument := range arguments {
		result[index] = strings.ReplaceAll(argument, "{{RUN_DIR}}", filepath.ToSlash(runDirectory))
	}
	return result
}

func exitAllowed(allowed []int, code int) bool {
	for _, candidate := range allowed {
		if candidate == code {
			return true
		}
	}
	return false
}

func cleanAbsolute(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", ErrInvalidPlan
	}
	result, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", ErrInvalidPlan
	}
	return result, nil
}

func pathsOverlap(left, right string) bool {
	return pathWithin(left, right) || pathWithin(right, left)
}

func pathWithin(child, parent string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))))
}

func hashFile(path string) (string, int64, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", 0, ErrInvalidPlan
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	bytesWritten, err := io.Copy(hash, file)
	if err != nil || bytesWritten != info.Size() {
		return "", 0, ErrInvalidPlan
	}
	return hex.EncodeToString(hash.Sum(nil)), info.Size(), nil
}

func digest(encoded []byte) string {
	value := sha256.Sum256(encoded)
	return hex.EncodeToString(value[:])
}

func writeExclusive(path string, encoded []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	written, writeErr := file.Write(encoded)
	if writeErr == nil && written != len(encoded) {
		writeErr = io.ErrShortWrite
	}
	syncErr, closeErr := file.Sync(), file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

type boundedWriter struct {
	writer    io.Writer
	remaining int
	truncated bool
}

func (writer *boundedWriter) Write(payload []byte) (int, error) {
	requested := len(payload)
	if requested == 0 {
		return 0, nil
	}
	if writer.remaining <= 0 {
		writer.truncated = true
		return requested, nil
	}
	chunk := payload
	if len(chunk) > writer.remaining {
		chunk, writer.truncated = chunk[:writer.remaining], true
	}
	written, err := writer.writer.Write(chunk)
	writer.remaining -= written
	if err != nil {
		return written, err
	}
	if written != len(chunk) {
		return written, io.ErrShortWrite
	}
	return requested, nil
}
