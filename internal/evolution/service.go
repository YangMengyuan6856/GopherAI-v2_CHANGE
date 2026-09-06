package evolution

import (
	"context"
	"errors"
	"sort"

	"GopherAI/internal/failurepool"
	"GopherAI/model"
)

type FailureSource interface {
	Latest(context.Context) (model.FailureMiningRun, []model.FailureCluster, []model.FailureImprovementProposal, bool, error)
}

type Service struct {
	repository Repository
	source     FailureSource
}

func NewService(repository Repository, source FailureSource) (*Service, error) {
	if repository == nil || source == nil {
		return nil, errors.New("evolution repository and failure source are required")
	}
	return &Service{repository: repository, source: source}, nil
}

func (service *Service) Audit(ctx context.Context) (Audit, error) {
	if service == nil || service.repository == nil {
		return Audit{}, errors.New("harness evolution service is unavailable")
	}
	artifacts, reviews, approved, applied, rows, err := service.repository.Audit(ctx, 20)
	if err != nil {
		return Audit{}, err
	}
	views := make([]ArtifactView, 0, len(rows))
	for _, row := range rows {
		patch, err := decodePatch(row.PatchJSON)
		if err != nil {
			return Audit{}, err
		}
		budget, err := decodeBudget(row.BudgetJSON)
		if err != nil {
			return Audit{}, err
		}
		views = append(views, ArtifactView{
			ID: row.ID, ArtifactType: row.ArtifactType, ArtifactVersion: row.ArtifactVersion, ParentVersion: row.ParentVersion,
			ParentSHA256: row.ParentSHA256, Patch: patch, PatchSHA256: row.PatchSHA256, TargetClusterID: row.TargetClusterID,
			SourceProposalSHA256: row.SourceProposalSHA256, ProposerVersion: row.ProposerVersion, ValidatorVersion: row.ValidatorVersion,
			DataSplit: row.DataSplit, DataVersion: row.DataVersion, DataSHA256: row.DataSHA256, Budget: budget, Status: row.Status,
			StaticValidationPassed: row.StaticValidationPassed, RequiresHumanApproval: row.RequiresHumanApproval,
			OfflineEvaluationPassed: row.OfflineEvaluationPassed, HoldoutOpened: row.HoldoutOpened, Applied: row.Applied,
			RollbackVersion: row.RollbackVersion, ArtifactSHA256: row.ArtifactSHA256, CreatedAt: row.CreatedAt,
		})
	}
	return Audit{
		SchemaVersion: SchemaVersion, Mode: Mode, ArtifactCount: artifacts, ReviewCount: reviews, ApprovedCount: approved,
		AppliedCount: applied, ActivePointers: 0, Latest: views,
		Guardrails:  []string{"failure_metadata_only", "allowlisted_artifact_types", "single_variable_patch", "static_validation_required", "append_only_lineage", "human_approval_required", "no_active_pointer", "no_source_or_permission_changes"},
		Limitations: []string{"当前只完成候选生成与静态门禁，尚未运行离线 A/B、validation 或 sealed holdout。", "确定性 Proposer 不调用模型；候选存在不代表自进化取得质量收益。"},
	}, nil
}

func (service *Service) MaterializeLatest(ctx context.Context) (MaterializeResult, error) {
	if service == nil || service.repository == nil || service.source == nil {
		return MaterializeResult{}, errors.New("harness evolution service is unavailable")
	}
	run, clusters, proposals, found, err := service.source.Latest(ctx)
	if err != nil {
		return MaterializeResult{}, err
	}
	if !found || run.SchemaVersion != failurepool.SchemaVersion {
		return MaterializeResult{}, errors.New("no failure-pool snapshot is available")
	}
	clusterByID := make(map[string]model.FailureCluster, len(clusters))
	for _, cluster := range clusters {
		clusterByID[cluster.ID] = cluster
	}
	sort.Slice(proposals, func(i, j int) bool { return proposals[i].ID < proposals[j].ID })
	result := MaterializeResult{SchemaVersion: SchemaVersion, SourceRunID: run.ID, ProposalCount: len(proposals), Skipped: []SkipSummary{}}
	for _, proposal := range proposals {
		cluster, exists := clusterByID[proposal.ClusterID]
		if !exists {
			return MaterializeResult{}, errors.New("failure proposal references an unknown cluster")
		}
		artifact, buildErr := BuildCandidate(run, cluster, proposal)
		if buildErr != nil {
			reason := "not_harness_artifact"
			if !errors.Is(buildErr, ErrNotHarnessArtifact) {
				reason = "static_validation_failed"
			}
			result.Skipped = append(result.Skipped, SkipSummary{ProposalID: proposal.ID, ReasonCode: reason})
			continue
		}
		created, createErr := service.repository.CreateArtifact(ctx, artifact)
		if createErr != nil {
			return MaterializeResult{}, createErr
		}
		if created {
			result.CreatedCount++
		} else {
			result.ExistingCount++
		}
	}
	result.Audit, err = service.Audit(ctx)
	return result, err
}
