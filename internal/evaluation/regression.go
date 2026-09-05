package evaluation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	RegressionGateVersion       = "regression-gate-v1"
	BaselineSnapshotVersion     = "evaluation-snapshot-v1"
	MaximumRelativeRegression   = .05
	MinimumEvaluationCompletion = .98
)

type EvaluationVersionLock struct {
	DatasetVersion string `json:"dataset_version"`
	DatasetSHA256  string `json:"dataset_sha256"`
	FixtureSHA256  string `json:"fixture_sha256,omitempty"`
	ModelVersion   string `json:"model_version,omitempty"`
	PromptVersion  string `json:"prompt_version,omitempty"`
	JudgeVersion   string `json:"judge_version,omitempty"`
	Environment    string `json:"environment"`
}

type SnapshotMetric struct {
	Name           string  `json:"name"`
	Slice          string  `json:"slice"`
	Value          float64 `json:"value"`
	Direction      string  `json:"direction"`
	AbsoluteTarget float64 `json:"absolute_target"`
	Critical       bool    `json:"critical"`
	SafetyCritical bool    `json:"safety_critical"`
}

type EvaluationSnapshot struct {
	SchemaVersion        string                `json:"schema_version"`
	SnapshotID           string                `json:"snapshot_id"`
	CandidateVersion     string                `json:"candidate_version"`
	CreatedAt            time.Time             `json:"created_at"`
	VersionLock          EvaluationVersionLock `json:"version_lock"`
	CaseCount            int                   `json:"case_count"`
	CompletionRate       float64               `json:"completion_rate"`
	TechnicalGatesPassed bool                  `json:"technical_gates_passed"`
	HumanReviewed        bool                  `json:"human_reviewed"`
	Metrics              []SnapshotMetric      `json:"metrics"`
}

type RegressionMetricResult struct {
	Name               string   `json:"name"`
	Slice              string   `json:"slice"`
	Baseline           float64  `json:"baseline"`
	Candidate          float64  `json:"candidate"`
	Direction          string   `json:"direction"`
	AbsoluteTarget     float64  `json:"absolute_target"`
	RelativeRegression float64  `json:"relative_regression"`
	AbsolutePassed     bool     `json:"absolute_passed"`
	RelativePassed     bool     `json:"relative_passed"`
	SafetyPassed       bool     `json:"safety_passed"`
	Passed             bool     `json:"passed"`
	ReasonCodes        []string `json:"reason_codes,omitempty"`
}

type RegressionReport struct {
	SchemaVersion       string                   `json:"schema_version"`
	GateVersion         string                   `json:"gate_version"`
	BaselineSnapshotID  string                   `json:"baseline_snapshot_id"`
	CandidateSnapshotID string                   `json:"candidate_snapshot_id"`
	GeneratedAt         time.Time                `json:"generated_at"`
	VersionLockMatched  bool                     `json:"version_lock_matched"`
	CompletionPassed    bool                     `json:"completion_passed"`
	CandidateTechnical  bool                     `json:"candidate_technical_gates_passed"`
	CandidateReviewed   bool                     `json:"candidate_human_reviewed"`
	Blocked             bool                     `json:"blocked"`
	GateFailures        []string                 `json:"gate_failures,omitempty"`
	Metrics             []RegressionMetricResult `json:"metrics"`
}

type BaselineStore interface {
	Freeze(snapshot EvaluationSnapshot) (string, error)
	Load(snapshotID string) (EvaluationSnapshot, string, error)
}

type FileBaselineStore struct{ directory string }

func NewFileBaselineStore(directory string) (*FileBaselineStore, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return nil, errors.New("baseline directory is required")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	return &FileBaselineStore{directory: absolute}, nil
}

func (store *FileBaselineStore) Freeze(snapshot EvaluationSnapshot) (string, error) {
	if store == nil {
		return "", errors.New("baseline store is required")
	}
	if err := validateEvaluationSnapshot(snapshot); err != nil {
		return "", err
	}
	if !snapshot.HumanReviewed || !snapshot.TechnicalGatesPassed || snapshot.CompletionRate < MinimumEvaluationCompletion {
		return "", errors.New("only reviewed, technically passing and complete snapshots may be frozen")
	}
	encoded, err := canonicalSnapshotJSON(snapshot)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(store.directory, 0o755); err != nil {
		return "", err
	}
	path, err := store.snapshotPath(snapshot.SnapshotID)
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o444)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", errors.New("baseline snapshot is immutable and already exists")
		}
		return "", err
	}
	_, writeErr := file.Write(encoded)
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (store *FileBaselineStore) Load(snapshotID string) (EvaluationSnapshot, string, error) {
	var snapshot EvaluationSnapshot
	if store == nil {
		return snapshot, "", errors.New("baseline store is required")
	}
	path, err := store.snapshotPath(snapshotID)
	if err != nil {
		return snapshot, "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return snapshot, "", err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, 16<<20))
	if err != nil {
		return snapshot, "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return snapshot, "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return snapshot, "", errors.New("baseline snapshot has trailing content")
	}
	if err := validateEvaluationSnapshot(snapshot); err != nil {
		return snapshot, "", err
	}
	digest := sha256.Sum256(encoded)
	return snapshot, hex.EncodeToString(digest[:]), nil
}

func (store *FileBaselineStore) snapshotPath(snapshotID string) (string, error) {
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" || strings.ContainsAny(snapshotID, `/\`) || snapshotID == "." || snapshotID == ".." {
		return "", errors.New("snapshot id is invalid")
	}
	return filepath.Join(store.directory, snapshotID+".json"), nil
}

func EvaluateRegression(baseline EvaluationSnapshot, candidate EvaluationSnapshot, generatedAt time.Time) (RegressionReport, error) {
	if err := validateEvaluationSnapshot(baseline); err != nil {
		return RegressionReport{}, fmt.Errorf("baseline: %w", err)
	}
	if err := validateEvaluationSnapshot(candidate); err != nil {
		return RegressionReport{}, fmt.Errorf("candidate: %w", err)
	}
	report := RegressionReport{
		SchemaVersion: "regression-report-v1", GateVersion: RegressionGateVersion,
		BaselineSnapshotID: baseline.SnapshotID, CandidateSnapshotID: candidate.SnapshotID, GeneratedAt: generatedAt.UTC(),
		VersionLockMatched: baseline.VersionLock == candidate.VersionLock,
		CompletionPassed:   candidate.CompletionRate >= MinimumEvaluationCompletion,
		CandidateTechnical: candidate.TechnicalGatesPassed, CandidateReviewed: candidate.HumanReviewed,
		Metrics: []RegressionMetricResult{},
	}
	if !report.VersionLockMatched {
		report.GateFailures = append(report.GateFailures, "version_lock_mismatch")
	}
	if !report.CompletionPassed {
		report.GateFailures = append(report.GateFailures, "completion_rate_below_0.98")
	}
	if !report.CandidateTechnical {
		report.GateFailures = append(report.GateFailures, "candidate_technical_gate_failed")
	}
	if !report.CandidateReviewed {
		report.GateFailures = append(report.GateFailures, "candidate_labels_not_human_reviewed")
	}
	candidateMetrics := snapshotMetricMap(candidate.Metrics)
	for _, baselineMetric := range baseline.Metrics {
		key := metricSnapshotKey(baselineMetric)
		candidateMetric, exists := candidateMetrics[key]
		if !exists {
			report.GateFailures = append(report.GateFailures, "candidate_metric_missing:"+key)
			continue
		}
		metricResult := compareSnapshotMetric(baselineMetric, candidateMetric)
		report.Metrics = append(report.Metrics, metricResult)
		if baselineMetric.Critical && !metricResult.Passed {
			report.GateFailures = append(report.GateFailures, "metric_regression:"+key)
		}
		delete(candidateMetrics, key)
	}
	for key := range candidateMetrics {
		report.GateFailures = append(report.GateFailures, "candidate_metric_not_in_baseline:"+key)
	}
	sort.Strings(report.GateFailures)
	report.Blocked = len(report.GateFailures) > 0
	return report, nil
}

func compareSnapshotMetric(baseline SnapshotMetric, candidate SnapshotMetric) RegressionMetricResult {
	result := RegressionMetricResult{
		Name: baseline.Name, Slice: baseline.Slice, Baseline: baseline.Value, Candidate: candidate.Value,
		Direction: baseline.Direction, AbsoluteTarget: baseline.AbsoluteTarget,
		AbsolutePassed: metricMeetsTarget(candidate.Value, baseline.Direction, baseline.AbsoluteTarget),
		RelativePassed: true, SafetyPassed: true,
	}
	if candidate.Direction != baseline.Direction || candidate.AbsoluteTarget != baseline.AbsoluteTarget || candidate.SafetyCritical != baseline.SafetyCritical {
		result.ReasonCodes = append(result.ReasonCodes, "metric_contract_changed")
		result.RelativePassed = false
	}
	result.RelativeRegression = relativeRegression(baseline.Value, candidate.Value, baseline.Direction)
	if result.RelativeRegression > MaximumRelativeRegression {
		result.RelativePassed = false
		result.ReasonCodes = append(result.ReasonCodes, "relative_regression_above_0.05")
	}
	if !result.AbsolutePassed {
		result.ReasonCodes = append(result.ReasonCodes, "absolute_target_failed")
	}
	if baseline.SafetyCritical && candidate.Value != 0 {
		result.SafetyPassed = false
		result.ReasonCodes = append(result.ReasonCodes, "safety_metric_nonzero")
	}
	result.Passed = result.AbsolutePassed && result.RelativePassed && result.SafetyPassed
	result.ReasonCodes = uniqueSorted(result.ReasonCodes)
	return result
}

func relativeRegression(baseline float64, candidate float64, direction string) float64 {
	denominator := math.Abs(baseline)
	if denominator < 1e-12 {
		if candidate == baseline {
			return 0
		}
		return math.Inf(1)
	}
	switch direction {
	case MetricHigherIsBetter:
		return (baseline - candidate) / denominator
	case MetricLowerIsBetter, MetricMustBeZero:
		return (candidate - baseline) / denominator
	default:
		return math.Inf(1)
	}
}

func SnapshotFromScorecard(snapshotID string, versionLock EvaluationVersionLock, scorecard DeterministicScorecard) EvaluationSnapshot {
	metrics := []SnapshotMetric{}
	for _, slice := range scorecard.Slices {
		for _, metric := range slice.Metrics {
			metrics = append(metrics, SnapshotMetric{
				Name: metric.Name, Slice: metric.Slice, Value: metric.Value, Direction: metric.Direction,
				AbsoluteTarget: metric.AbsoluteTarget, Critical: metric.Critical, SafetyCritical: metric.Direction == MetricMustBeZero,
			})
		}
	}
	sort.Slice(metrics, func(left, right int) bool {
		return metricSnapshotKey(metrics[left]) < metricSnapshotKey(metrics[right])
	})
	return EvaluationSnapshot{
		SchemaVersion: BaselineSnapshotVersion, SnapshotID: snapshotID, CandidateVersion: scorecard.CandidateVersion,
		CreatedAt: scorecard.GeneratedAt, VersionLock: versionLock, CaseCount: scorecard.CaseCount,
		CompletionRate: scorecard.CompletionRate, TechnicalGatesPassed: scorecard.TechnicalGatesPassed,
		HumanReviewed: scorecard.HumanReviewed, Metrics: metrics,
	}
}

func validateEvaluationSnapshot(snapshot EvaluationSnapshot) error {
	if snapshot.SchemaVersion != BaselineSnapshotVersion || strings.TrimSpace(snapshot.SnapshotID) == "" || strings.TrimSpace(snapshot.CandidateVersion) == "" || snapshot.CreatedAt.IsZero() || snapshot.CaseCount < 1 || len(snapshot.Metrics) == 0 {
		return errors.New("snapshot identity, version, time, cases and metrics are required")
	}
	if snapshot.CompletionRate < 0 || snapshot.CompletionRate > 1 || strings.TrimSpace(snapshot.VersionLock.DatasetVersion) == "" || len(snapshot.VersionLock.DatasetSHA256) != 64 || strings.TrimSpace(snapshot.VersionLock.Environment) == "" {
		return errors.New("snapshot completion or version lock is invalid")
	}
	seen := make(map[string]struct{}, len(snapshot.Metrics))
	for _, metric := range snapshot.Metrics {
		key := metricSnapshotKey(metric)
		if strings.TrimSpace(metric.Name) == "" || strings.TrimSpace(metric.Slice) == "" || (metric.Direction != MetricHigherIsBetter && metric.Direction != MetricLowerIsBetter && metric.Direction != MetricMustBeZero) || math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) {
			return fmt.Errorf("snapshot metric %s is invalid", key)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate snapshot metric %s", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func snapshotMetricMap(metrics []SnapshotMetric) map[string]SnapshotMetric {
	result := make(map[string]SnapshotMetric, len(metrics))
	for _, metric := range metrics {
		result[metricSnapshotKey(metric)] = metric
	}
	return result
}

func metricSnapshotKey(metric SnapshotMetric) string { return metric.Slice + "/" + metric.Name }

func canonicalSnapshotJSON(snapshot EvaluationSnapshot) ([]byte, error) {
	clone := snapshot
	clone.Metrics = append([]SnapshotMetric{}, snapshot.Metrics...)
	sort.Slice(clone.Metrics, func(left, right int) bool {
		return metricSnapshotKey(clone.Metrics[left]) < metricSnapshotKey(clone.Metrics[right])
	})
	encoded, err := json.MarshalIndent(clone, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}
