package evolution

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/failurepool"
	"GopherAI/model"
)

func TestBuildCandidateCreatesSingleVariableImmutableLineage(t *testing.T) {
	run, cluster, proposal := evolutionFixture("prompt", "resolution_output_contract")
	artifact, err := BuildCandidate(run, cluster, proposal)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCandidate(artifact); err != nil {
		t.Fatal(err)
	}
	patch, err := decodePatch(artifact.PatchJSON)
	if err != nil {
		t.Fatal(err)
	}
	if patch.VariableCount != 1 || patch.Path != "/response_contract/require_verification_step" || artifact.Applied || artifact.HoldoutOpened || artifact.OfflineEvaluationPassed || !artifact.RequiresHumanApproval {
		t.Fatalf("candidate crossed the offline boundary: %+v patch=%+v", artifact, patch)
	}
	if artifact.CreatedAt != proposal.CreatedAt || artifact.BudgetJSON != `{"model_calls":0,"token_budget":0,"search_iterations":1}` {
		t.Fatalf("candidate provenance or budget is not reproducible: %+v", artifact)
	}
	tampered := artifact
	tampered.PatchJSON = `{"operation":"replace","path":"/tool_permissions/admin","value":true,"variable_count":1}`
	if err := ValidateCandidate(tampered); err == nil {
		t.Fatal("forbidden patch was accepted")
	}
	tampered = artifact
	tampered.BudgetJSON = `{"model_calls":1,"token_budget":0,"search_iterations":1}`
	if err := ValidateCandidate(tampered); err == nil {
		t.Fatal("candidate generation was allowed to spend model budget")
	}
}

func TestBuildCandidateRejectsProposalThatAlreadyCrossedAGate(t *testing.T) {
	run, cluster, proposal := evolutionFixture("prompt", "grounded_answer_contract")
	proposal.OfflineGatePassed = true
	if _, err := BuildCandidate(run, cluster, proposal); !errors.Is(err, ErrInvalidLineage) {
		t.Fatalf("pre-evaluated proposal was accepted: %v", err)
	}
}

func TestDatasetProposalIsNotTurnedIntoHarnessArtifact(t *testing.T) {
	run, cluster, proposal := evolutionFixture("dataset", "user_rejected_answer_case")
	if _, err := BuildCandidate(run, cluster, proposal); err == nil {
		t.Fatal("dataset proposal was incorrectly materialized as a harness artifact")
	}
}

func TestMaterializeLatestIsIdempotentAndKeepsNoActivePointer(t *testing.T) {
	run, cluster, prompt := evolutionFixture("prompt", "resolution_output_contract")
	_, _, dataset := evolutionFixture("dataset", "user_rejected_answer_case")
	dataset.RunID, dataset.ClusterID = run.ID, cluster.ID
	repository := &memoryRepository{}
	service, err := NewService(repository, staticFailureSource{run: run, clusters: []model.FailureCluster{cluster}, proposals: []model.FailureImprovementProposal{prompt, dataset}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.MaterializeLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.MaterializeLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.CreatedCount != 1 || len(first.Skipped) != 1 || second.ExistingCount != 1 || first.Audit.ActivePointers != 0 || first.Audit.AppliedCount != 0 || len(repository.rows) != 1 {
		t.Fatalf("unexpected materialization: first=%+v second=%+v rows=%d", first, second, len(repository.rows))
	}
}

func evolutionFixture(kind, target string) (model.FailureMiningRun, model.FailureCluster, model.FailureImprovementProposal) {
	runID := strings.Repeat("a", 64)
	clusterID := strings.Repeat("b", 64)
	proposalID := strings.Repeat("c", 64)
	now := time.Unix(100, 0).UTC()
	return model.FailureMiningRun{ID: runID, InputHash: strings.Repeat("e", 64), SchemaVersion: failurepool.SchemaVersion, Status: failurepool.StatusCompleted},
		model.FailureCluster{ID: clusterID, RunID: runID, SchemaVersion: failurepool.SchemaVersion, SampleCount: 1, SampleSetHash: strings.Repeat("d", 64), Status: failurepool.StatusPendingReview},
		model.FailureImprovementProposal{ID: proposalID, ProposalHash: proposalID, RunID: runID, ClusterID: clusterID, SchemaVersion: failurepool.SchemaVersion, CandidateKind: kind, Target: target, State: failurepool.StatusPendingReview, RequiresHumanReview: true, CreatedAt: now}
}

type staticFailureSource struct {
	run       model.FailureMiningRun
	clusters  []model.FailureCluster
	proposals []model.FailureImprovementProposal
}

func (source staticFailureSource) Latest(context.Context) (model.FailureMiningRun, []model.FailureCluster, []model.FailureImprovementProposal, bool, error) {
	return source.run, source.clusters, source.proposals, true, nil
}

type memoryRepository struct{ rows []model.HarnessArtifact }

func (repository *memoryRepository) CreateArtifact(_ context.Context, artifact model.HarnessArtifact) (bool, error) {
	for _, row := range repository.rows {
		if row.ID == artifact.ID {
			return false, nil
		}
	}
	repository.rows = append(repository.rows, artifact)
	return true, nil
}

func (*memoryRepository) AppendReview(context.Context, model.HarnessArtifactReview) (bool, error) {
	return false, nil
}

func (repository *memoryRepository) Audit(context.Context, int) (int64, int64, int64, int64, []model.HarnessArtifact, error) {
	return int64(len(repository.rows)), 0, 0, 0, append([]model.HarnessArtifact(nil), repository.rows...), nil
}
