package failurepool

import (
	"GopherAI/internal/onlineeval"
	"GopherAI/model"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	SchemaVersion         = "failure-pool-v1"
	MinerVersion          = "where-why-miner-v1"
	StatusCompleted       = "completed"
	StatusPendingReview   = "pending_human_review"
	WindowDays            = 7
	MaximumSourceSamples  = 1000
	LowOverallScoreCutoff = 0.60
)

type MinedSnapshot struct {
	Run       model.FailureMiningRun
	Clusters  []model.FailureCluster
	Proposals []model.FailureImprovementProposal
}

type failureDimension struct {
	where, why, reason string
}

type clusterAccumulator struct {
	dimension failureDimension
	intent    string
	strategy  string
	samples   []string
}

func Mine(samples []model.OnlineEvaluationSample, windowStart, windowEnd time.Time) MinedSnapshot {
	windowStart, windowEnd = windowStart.UTC(), windowEnd.UTC()
	ordered := append([]model.OnlineEvaluationSample(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].CreatedAt.Before(ordered[j].CreatedAt)
	})
	if len(ordered) > MaximumSourceSamples {
		ordered = ordered[len(ordered)-MaximumSourceSamples:]
	}
	groups := make(map[string]*clusterAccumulator)
	inputIdentities := make([]string, 0, len(ordered))
	eligibleCount := 0
	for _, sample := range ordered {
		inputIdentities = append(inputIdentities, sample.ID+":"+sample.PayloadHash)
		dimension, eligible := classify(sample)
		if !eligible {
			continue
		}
		eligibleCount++
		intent := bounded(sample.Intent, "unknown")
		strategy := bounded(sample.Strategy, "unknown")
		key := strings.Join([]string{intent, strategy, dimension.where, dimension.why, dimension.reason}, "\x00")
		group := groups[key]
		if group == nil {
			group = &clusterAccumulator{dimension: dimension, intent: intent, strategy: strategy}
			groups[key] = group
		}
		group.samples = append(group.samples, sample.ID+":"+sample.PayloadHash)
	}
	sort.Strings(inputIdentities)
	inputHash := digest(strings.Join(inputIdentities, "\n"))
	runID := digest(MinerVersion + "\x00" + inputHash)
	run := model.FailureMiningRun{
		ID: runID, SchemaVersion: SchemaVersion, MinerVersion: MinerVersion, InputHash: inputHash,
		WindowStart: windowStart, WindowEnd: windowEnd, SampleCount: len(ordered), EligibleCount: eligibleCount,
		Status: StatusCompleted, CreatedAt: windowEnd,
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	snapshot := MinedSnapshot{Run: run, Clusters: make([]model.FailureCluster, 0, len(keys)), Proposals: make([]model.FailureImprovementProposal, 0, len(keys))}
	for _, key := range keys {
		group := groups[key]
		sort.Strings(group.samples)
		sampleSetHash := digest(strings.Join(group.samples, "\n"))
		clusterID := digest(runID + "\x00" + key + "\x00" + sampleSetHash)
		cluster := model.FailureCluster{
			ID: clusterID, RunID: runID, SchemaVersion: SchemaVersion,
			WhereCode: group.dimension.where, WhyCode: group.dimension.why, PrimaryReason: group.dimension.reason,
			Intent: group.intent, Strategy: group.strategy, SampleCount: len(group.samples), SampleSetHash: sampleSetHash,
			Status: StatusPendingReview, CreatedAt: windowEnd,
		}
		proposal := buildProposal(runID, cluster, windowEnd)
		snapshot.Clusters = append(snapshot.Clusters, cluster)
		snapshot.Proposals = append(snapshot.Proposals, proposal)
	}
	snapshot.Run.ClusterCount = len(snapshot.Clusters)
	snapshot.Run.ProposalCount = len(snapshot.Proposals)
	return snapshot
}

func classify(sample model.OnlineEvaluationSample) (failureDimension, bool) {
	if sample.Simulation || (sample.Status != onlineeval.StatusCompleted && sample.Status != onlineeval.StatusJudgeFailed && sample.Status != onlineeval.StatusDead) {
		return failureDimension{}, false
	}
	var reasons []string
	_ = json.Unmarshal([]byte(sample.SampleReasonsJSON), &reasons)
	priority := []struct {
		reason string
		value  failureDimension
	}{
		{"user_downvote", failureDimension{"answer_quality", "user_rejected_answer", "user_downvote"}},
		{"budget_exceeded", failureDimension{"agent_budget", "execution_budget_exceeded", "budget_exceeded"}},
		{"tool_failure", failureDimension{"tool_runtime", "governed_tool_failure", "tool_failure"}},
		{"evidence_gate_failed", failureDimension{"retrieval_evidence_gate", "insufficient_or_missing_evidence", "evidence_gate_failed"}},
		{"low_confidence", failureDimension{"answer_generation", "low_model_confidence", "low_confidence"}},
		{"unresolved", failureDimension{"task_resolution", "unresolved_response", "unresolved"}},
		{"request_error", failureDimension{"request_execution", "request_error", "request_error"}},
	}
	for _, candidate := range priority {
		if contains(reasons, candidate.reason) {
			return candidate.value, true
		}
	}
	if sample.Status == onlineeval.StatusCompleted && sample.Overall < LowOverallScoreCutoff {
		return failureDimension{"answer_quality", "judge_score_below_threshold", "judge_low_quality"}, true
	}
	return failureDimension{}, false
}

func buildProposal(runID string, cluster model.FailureCluster, now time.Time) model.FailureImprovementProposal {
	kind, target := proposalTarget(cluster.PrimaryReason)
	change := map[string]string{
		"operation": "create_candidate", "scope": "single_variable", "target": target,
		"source": "sanitized_failure_metadata", "activation": "forbidden",
	}
	encoded, _ := json.Marshal(change)
	proposalHash := digest(strings.Join([]string{SchemaVersion, cluster.ID, kind, target, string(encoded)}, "\x00"))
	return model.FailureImprovementProposal{
		ID: proposalHash, RunID: runID, ClusterID: cluster.ID, SchemaVersion: SchemaVersion,
		CandidateKind: kind, Target: target, ChangeJSON: string(encoded), RationaleCode: cluster.WhyCode,
		State: StatusPendingReview, RequiresHumanReview: true, OfflineGatePassed: false,
		IsolationCanaryPassed: false, Applied: false, ProposalHash: proposalHash, CreatedAt: now,
	}
}

func proposalTarget(reason string) (string, string) {
	switch reason {
	case "user_downvote":
		return "dataset", "user_rejected_answer_case"
	case "evidence_gate_failed":
		return "dataset", "rag_insufficient_evidence_case"
	case "tool_failure":
		return "rule", "tool_failure_recovery_rule"
	case "budget_exceeded":
		return "parameter", "context_budget_allocation"
	case "low_confidence":
		return "dataset", "low_confidence_boundary_case"
	case "unresolved":
		return "prompt", "resolution_output_contract"
	case "request_error":
		return "rule", "request_fallback_classification"
	default:
		return "prompt", "grounded_answer_contract"
	}
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func bounded(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	runes := []rune(value)
	if len(runes) > 64 {
		return fmt.Sprintf("bounded-%s", digest(value)[:16])
	}
	return value
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
