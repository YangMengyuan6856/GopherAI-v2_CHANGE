package failurepool

import (
	"GopherAI/internal/onlineeval"
	"GopherAI/model"
	"context"
	"reflect"
	"testing"
	"time"
)

func TestAcceptanceCoversWhereWhyAndNonExecutableCandidates(t *testing.T) {
	report := RunAcceptance(time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC))
	if !report.Passed || report.SourceSamples != 9 || report.EligibleSamples != 8 || report.ClusterCount != 8 || report.ProposalCount != 8 || report.RawContentRead || report.ActivePolicyWrite {
		t.Fatalf("failure-pool acceptance did not preserve its boundary: %+v", report)
	}
	for _, item := range report.Cases {
		if !item.Passed || item.WhereCode == "" || item.WhyCode == "" || item.CandidateKind == "" || item.Target == "" {
			t.Fatalf("incomplete acceptance case: %+v", item)
		}
	}
}

func TestMiningIsDeterministicAndDoesNotReadRawContent(t *testing.T) {
	now := time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)
	sample := model.OnlineEvaluationSample{
		ID: "sample-1", PayloadHash: digest("payload"), Intent: "project_qa", Strategy: "rag_fast",
		SampleReasonsJSON: `["evidence_gate_failed"]`, Status: onlineeval.StatusCompleted, Overall: .1, CreatedAt: now,
		Question: "private question one", Answer: "private answer one", EvidenceJSON: `[{"content":"private"}]`,
	}
	first := Mine([]model.OnlineEvaluationSample{sample}, now.Add(-24*time.Hour), now)
	sample.Question, sample.Answer, sample.EvidenceJSON = "entirely different", "entirely different", `[{"content":"different"}]`
	second := Mine([]model.OnlineEvaluationSample{sample}, now.Add(-24*time.Hour), now)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("raw content changed metadata-only mining result:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if first.Run.EligibleCount != 1 || first.Clusters[0].WhereCode != "retrieval_evidence_gate" || first.Proposals[0].Applied || !first.Proposals[0].RequiresHumanReview {
		t.Fatalf("unexpected evidence failure classification: %+v", first)
	}
}

func TestHealthyAndJudgeInfrastructureSamplesDoNotBecomeProductCandidates(t *testing.T) {
	now := time.Now().UTC()
	samples := []model.OnlineEvaluationSample{
		{ID: "healthy", PayloadHash: digest("healthy"), Intent: "general", Strategy: "legacy_chat", SampleReasonsJSON: `["stable_rate"]`, Status: onlineeval.StatusCompleted, Overall: .95, CreatedAt: now},
		{ID: "judge-failed", PayloadHash: digest("judge"), Intent: "general", Strategy: "legacy_chat", SampleReasonsJSON: `["stable_rate"]`, Status: onlineeval.StatusJudgeFailed, CreatedAt: now},
	}
	snapshot := Mine(samples, now.Add(-time.Hour), now)
	if snapshot.Run.SampleCount != 2 || snapshot.Run.EligibleCount != 0 || len(snapshot.Clusters) != 0 || len(snapshot.Proposals) != 0 {
		t.Fatalf("infrastructure/healthy observations became product proposals: %+v", snapshot)
	}
}

type memoryRepository struct {
	samples  []model.OnlineEvaluationSample
	snapshot MinedSnapshot
	found    bool
}

func (repository *memoryRepository) ListSamples(context.Context, time.Time, time.Time, int) ([]model.OnlineEvaluationSample, error) {
	return append([]model.OnlineEvaluationSample(nil), repository.samples...), nil
}

func (repository *memoryRepository) SaveSnapshot(_ context.Context, snapshot MinedSnapshot) (bool, string, error) {
	created := !repository.found || repository.snapshot.Run.InputHash != snapshot.Run.InputHash
	if created {
		repository.snapshot = snapshot
		repository.found = true
	}
	return created, repository.snapshot.Run.ID, nil
}

func (repository *memoryRepository) Latest(context.Context) (model.FailureMiningRun, []model.FailureCluster, []model.FailureImprovementProposal, bool, error) {
	return repository.snapshot.Run, repository.snapshot.Clusters, repository.snapshot.Proposals, repository.found, nil
}

func TestRunCyclePersistsOneImmutableSnapshotPerInputSet(t *testing.T) {
	now := time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)
	repository := &memoryRepository{samples: []model.OnlineEvaluationSample{{
		ID: "failure", PayloadHash: digest("failure"), Intent: "project_qa", Strategy: "rag_fast", SampleReasonsJSON: `["user_downvote"]`, Status: onlineeval.StatusCompleted, CreatedAt: now,
	}}}
	service := NewService(repository, func() time.Time { return now })
	first, err := service.RunCycle(context.Background())
	if err != nil || !first.Created || !first.HasRun || first.Run.EligibleCount != 1 || len(first.Proposals) != 1 {
		t.Fatalf("first cycle failed: result=%+v err=%v", first, err)
	}
	second, err := service.RunCycle(context.Background())
	if err != nil || second.Created || second.Run.ID != first.Run.ID {
		t.Fatalf("unchanged input created another snapshot: result=%+v err=%v", second, err)
	}
}

func TestRepositoryValidationRejectsExecutableProposal(t *testing.T) {
	now := time.Now().UTC()
	snapshot := Mine([]model.OnlineEvaluationSample{{ID: "failure", PayloadHash: digest("failure"), Intent: "general", Strategy: "legacy_chat", SampleReasonsJSON: `["user_downvote"]`, Status: onlineeval.StatusCompleted, CreatedAt: now}}, now.Add(-time.Hour), now)
	snapshot.Proposals[0].Applied = true
	if err := validateSnapshot(snapshot); err == nil {
		t.Fatal("repository accepted an already-applied failure proposal")
	}
}
