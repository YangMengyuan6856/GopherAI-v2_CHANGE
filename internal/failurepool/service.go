package failurepool

import (
	"GopherAI/internal/onlineeval"
	"GopherAI/model"
	"context"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"time"
)

type PublicProposal struct {
	ID                    string `json:"id"`
	ClusterID             string `json:"cluster_id"`
	CandidateKind         string `json:"candidate_kind"`
	Target                string `json:"target"`
	RationaleCode         string `json:"rationale_code"`
	State                 string `json:"state"`
	RequiresHumanReview   bool   `json:"requires_human_review"`
	OfflineGatePassed     bool   `json:"offline_gate_passed"`
	IsolationCanaryPassed bool   `json:"isolation_canary_passed"`
	Applied               bool   `json:"applied"`
	ProposalHash          string `json:"proposal_hash"`
}

type Audit struct {
	SchemaVersion string                  `json:"schema_version"`
	Mode          string                  `json:"mode"`
	MinerVersion  string                  `json:"miner_version"`
	WindowDays    int                     `json:"window_days"`
	HasRun        bool                    `json:"has_run"`
	Run           *model.FailureMiningRun `json:"run,omitempty"`
	Clusters      []model.FailureCluster  `json:"clusters"`
	Proposals     []PublicProposal        `json:"proposals"`
	Guardrails    []string                `json:"guardrails"`
}

type CycleResult struct {
	Audit
	Created bool `json:"created"`
}

type AcceptanceCase struct {
	Reason        string `json:"reason"`
	WhereCode     string `json:"where_code"`
	WhyCode       string `json:"why_code"`
	CandidateKind string `json:"candidate_kind"`
	Target        string `json:"target"`
	Passed        bool   `json:"passed"`
}

type AcceptanceReport struct {
	SchemaVersion     string           `json:"schema_version"`
	Simulation        bool             `json:"simulation"`
	Passed            bool             `json:"passed"`
	SourceSamples     int              `json:"source_samples"`
	EligibleSamples   int              `json:"eligible_samples"`
	ClusterCount      int              `json:"cluster_count"`
	ProposalCount     int              `json:"proposal_count"`
	Cases             []AcceptanceCase `json:"cases"`
	RawContentRead    bool             `json:"raw_content_read"`
	ActivePolicyWrite bool             `json:"active_policy_write"`
	Limitations       []string         `json:"limitations"`
}

type Service struct {
	repository Repository
	clock      func() time.Time
}

func NewService(repository Repository, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{repository: repository, clock: clock}
}

func (service *Service) RunCycle(ctx context.Context) (CycleResult, error) {
	if service == nil || service.repository == nil {
		return CycleResult{}, errors.New("failure pool repository is unavailable")
	}
	now := service.clock().UTC()
	since := now.AddDate(0, 0, -WindowDays)
	samples, err := service.repository.ListSamples(ctx, since, now, MaximumSourceSamples)
	if err != nil {
		return CycleResult{}, err
	}
	snapshot := Mine(samples, since, now)
	created, _, err := service.repository.SaveSnapshot(ctx, snapshot)
	if err != nil {
		return CycleResult{}, err
	}
	audit, err := service.Audit(ctx)
	return CycleResult{Audit: audit, Created: created}, err
}

func (service *Service) Audit(ctx context.Context) (Audit, error) {
	if service == nil || service.repository == nil {
		return Audit{}, errors.New("failure pool repository is unavailable")
	}
	run, clusters, proposals, found, err := service.repository.Latest(ctx)
	if err != nil {
		return Audit{}, err
	}
	audit := Audit{
		SchemaVersion: SchemaVersion, Mode: "slow_loop_shadow", MinerVersion: MinerVersion, WindowDays: WindowDays,
		HasRun: found, Clusters: clusters, Proposals: publicProposals(proposals),
		Guardrails: []string{"metadata_only_clustering", "raw_content_not_read", "immutable_candidates", "human_review_required", "offline_gate_required", "isolated_canary_required", "no_active_policy_write"},
	}
	if found {
		audit.Run = &run
	}
	return audit, nil
}

func RunAcceptance(now time.Time) AcceptanceReport {
	samples, expectations := acceptanceSamples(now.UTC())
	snapshot := Mine(samples, now.AddDate(0, 0, -WindowDays), now)
	byReason := make(map[string]model.FailureCluster, len(snapshot.Clusters))
	proposalByCluster := make(map[string]model.FailureImprovementProposal, len(snapshot.Proposals))
	for _, cluster := range snapshot.Clusters {
		byReason[cluster.PrimaryReason] = cluster
	}
	for _, proposal := range snapshot.Proposals {
		proposalByCluster[proposal.ClusterID] = proposal
	}
	cases := make([]AcceptanceCase, 0, len(expectations))
	passed := snapshot.Run.EligibleCount == len(expectations) && len(snapshot.Clusters) == len(expectations) && len(snapshot.Proposals) == len(expectations)
	for _, expected := range expectations {
		cluster, exists := byReason[expected.reason]
		proposal := proposalByCluster[cluster.ID]
		casePassed := exists && cluster.WhereCode == expected.where && cluster.WhyCode == expected.why && proposal.CandidateKind == expected.kind && proposal.Target == expected.target && proposal.RequiresHumanReview && !proposal.OfflineGatePassed && !proposal.IsolationCanaryPassed && !proposal.Applied
		passed = passed && casePassed
		cases = append(cases, AcceptanceCase{Reason: expected.reason, WhereCode: cluster.WhereCode, WhyCode: cluster.WhyCode, CandidateKind: proposal.CandidateKind, Target: proposal.Target, Passed: casePassed})
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Reason < cases[j].Reason })
	return AcceptanceReport{
		SchemaVersion: "failure-pool-acceptance-v1", Simulation: true, Passed: passed,
		SourceSamples: len(samples), EligibleSamples: snapshot.Run.EligibleCount, ClusterCount: len(snapshot.Clusters), ProposalCount: len(snapshot.Proposals), Cases: cases,
		RawContentRead: false, ActivePolicyWrite: false,
		Limitations: []string{"验收仅证明确定性分类、候选边界和安全门，不代表候选已提升质量。", "候选必须经过人工标签复核、离线回归和隔离 Canary；本阶段没有激活接口。"},
	}
}

func Run(ctx context.Context, service *Service, initialDelay, interval time.Duration, logger *log.Logger) {
	if service == nil || interval <= 0 {
		return
	}
	if logger == nil {
		logger = log.Default()
	}
	cycle := func() {
		cycleContext, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		result, err := service.RunCycle(cycleContext)
		if err != nil {
			logger.Print(`{"event":"failure_pool_mining","status":"error","error_code":"FAILURE_POOL_CYCLE_FAILED"}`)
			return
		}
		logger.Printf(`{"event":"failure_pool_mining","status":"success","created":%t,"samples":%d,"eligible":%d,"clusters":%d,"proposals":%d}`, result.Created, result.Run.SampleCount, result.Run.EligibleCount, len(result.Clusters), len(result.Proposals))
	}
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
		cycle()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cycle()
		}
	}
}

func publicProposals(input []model.FailureImprovementProposal) []PublicProposal {
	result := make([]PublicProposal, 0, len(input))
	for _, proposal := range input {
		result = append(result, PublicProposal{
			ID: proposal.ID, ClusterID: proposal.ClusterID, CandidateKind: proposal.CandidateKind, Target: proposal.Target,
			RationaleCode: proposal.RationaleCode, State: proposal.State, RequiresHumanReview: proposal.RequiresHumanReview,
			OfflineGatePassed: proposal.OfflineGatePassed, IsolationCanaryPassed: proposal.IsolationCanaryPassed,
			Applied: proposal.Applied, ProposalHash: proposal.ProposalHash,
		})
	}
	return result
}

type acceptanceExpectation struct{ reason, where, why, kind, target string }

func acceptanceSamples(now time.Time) ([]model.OnlineEvaluationSample, []acceptanceExpectation) {
	definitions := []acceptanceExpectation{
		{"user_downvote", "answer_quality", "user_rejected_answer", "dataset", "user_rejected_answer_case"},
		{"budget_exceeded", "agent_budget", "execution_budget_exceeded", "parameter", "context_budget_allocation"},
		{"tool_failure", "tool_runtime", "governed_tool_failure", "rule", "tool_failure_recovery_rule"},
		{"evidence_gate_failed", "retrieval_evidence_gate", "insufficient_or_missing_evidence", "dataset", "rag_insufficient_evidence_case"},
		{"low_confidence", "answer_generation", "low_model_confidence", "dataset", "low_confidence_boundary_case"},
		{"unresolved", "task_resolution", "unresolved_response", "prompt", "resolution_output_contract"},
		{"request_error", "request_execution", "request_error", "rule", "request_fallback_classification"},
		{"judge_low_quality", "answer_quality", "judge_score_below_threshold", "prompt", "grounded_answer_contract"},
	}
	samples := make([]model.OnlineEvaluationSample, 0, len(definitions)+1)
	for index, definition := range definitions {
		reasons := []string{definition.reason}
		if definition.reason == "judge_low_quality" {
			reasons = []string{"stable_rate"}
		}
		encoded, _ := json.Marshal(reasons)
		samples = append(samples, model.OnlineEvaluationSample{
			ID: digest(definition.reason), PayloadHash: digest("payload-" + definition.reason), Intent: "project_qa", Strategy: "rag_fast",
			SampleReasonsJSON: string(encoded), Status: onlineeval.StatusCompleted, Overall: .2, CreatedAt: now.Add(time.Duration(index) * time.Nanosecond),
		})
	}
	healthyReasons, _ := json.Marshal([]string{"stable_rate"})
	samples = append(samples, model.OnlineEvaluationSample{ID: digest("healthy"), PayloadHash: digest("healthy-payload"), Intent: "general", Strategy: "legacy_chat", SampleReasonsJSON: string(healthyReasons), Status: onlineeval.StatusCompleted, Overall: .95, CreatedAt: now})
	return samples, definitions
}
