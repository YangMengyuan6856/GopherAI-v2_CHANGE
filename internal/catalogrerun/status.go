package catalogrerun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const StatusSchemaVersion = "evaluation-catalog-rerun-status-v1"

type Status struct {
	SchemaVersion             string  `json:"schema_version"`
	SealID                    string  `json:"seal_id"`
	State                     string  `json:"state"`
	ActiveRunID               string  `json:"active_run_id,omitempty"`
	LatestRun                 *Report `json:"latest_run,omitempty"`
	EvidenceIntegrityVerified bool    `json:"evidence_integrity_verified"`
	NextGate                  string  `json:"next_gate"`
}

type Inspector struct {
	outputRoot string
}

func NewInspector(outputRoot string) *Inspector {
	return &Inspector{outputRoot: outputRoot}
}

func (inspector *Inspector) Status(ctx context.Context, sealID string) (Status, error) {
	if inspector == nil || ctx == nil || !validSealID(sealID) {
		return Status{}, ErrInvalidReport
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	root, err := cleanAbsolute(inspector.outputRoot)
	if err != nil {
		return Status{}, err
	}
	status := Status{
		SchemaVersion: StatusSchemaVersion,
		SealID:        sealID,
		State:         "not_started",
		NextGate:      "使用当前封存候选生成只读计划，核对 Release 与六个 Runner Hash 后再显式执行。",
	}
	base := filepath.Join(root, sealID)
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return status, nil
	}
	if err != nil {
		return Status{}, err
	}
	activeRunID, active, err := readActiveRun(filepath.Join(base, "active.lock"))
	if err != nil {
		return Status{}, err
	}
	runNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == "active.lock" && !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() || !validRunID(entry.Name()) {
			return Status{}, ErrInvalidReport
		}
		runNames = append(runNames, entry.Name())
	}
	sort.Strings(runNames)
	if active {
		status.State, status.ActiveRunID = "running", activeRunID
		status.NextGate = "等待当前固定六步技术重跑结束；不得删除活跃锁、并行启动同一 Seal 或修改运行目录。"
		return status, nil
	}
	if len(runNames) == 0 {
		return status, nil
	}
	latestName := runNames[len(runNames)-1]
	latestDirectory := filepath.Join(base, latestName)
	if _, err := os.Stat(filepath.Join(latestDirectory, "result.json")); errors.Is(err, os.ErrNotExist) {
		status.State, status.ActiveRunID = "execution_failed", latestName
		status.NextGate = "最新 Run 未产生最终报告；保留现场并检查日志，修复后创建新 Run，不得复用或覆盖。"
		return status, nil
	} else if err != nil {
		return Status{}, err
	}
	report, err := ValidateRunDirectory(latestDirectory)
	if err != nil || report.SealID != sealID {
		return Status{}, ErrInvalidReport
	}
	status.LatestRun, status.EvidenceIntegrityVerified = &report, true
	switch report.Status {
	case "failed":
		status.State = "execution_failed"
		status.NextGate = "检查失败步骤与有界日志；修复环境或实现后创建新 Run，失败证据不得覆盖。"
	case "completed_technical_gate_failed":
		status.State = "technical_gate_failed"
		status.NextGate = "分析统一报告失败簇并修正候选；技术门未通过，禁止进入基线审批。"
	case "completed_technical_pass":
		status.State = "technical_passed"
		status.NextGate = "完成 Judge 人工校准与独立基线审批；技术通过不会自动冻结基线或切流。"
	default:
		return Status{}, ErrInvalidReport
	}
	return status, nil
}

func readActiveRun(lockPath string) (string, bool, error) {
	info, err := os.Lstat(lockPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 256 {
		return "", false, ErrInvalidReport
	}
	encoded, err := os.ReadFile(lockPath)
	if err != nil {
		return "", false, err
	}
	runID := strings.TrimSpace(string(encoded))
	if !validRunID(runID) {
		return "", false, ErrInvalidReport
	}
	return runID, true, nil
}

func validSealID(value string) bool {
	if len(value) != len("catalog-seal-")+32 || !strings.HasPrefix(value, "catalog-seal-") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "catalog-seal-") {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validRunID(value string) bool {
	if len(value) < len("rerun-")+8 || !strings.HasPrefix(value, "rerun-") || strings.ContainsAny(value, "/\\") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "rerun-") {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') && character != 'T' && character != 'Z' && character != '.' && character != '-' {
			return false
		}
	}
	return true
}
