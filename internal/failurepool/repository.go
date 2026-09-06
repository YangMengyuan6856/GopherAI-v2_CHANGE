package failurepool

import (
	"GopherAI/model"
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

type Repository interface {
	ListSamples(context.Context, time.Time, time.Time, int) ([]model.OnlineEvaluationSample, error)
	SaveSnapshot(context.Context, MinedSnapshot) (bool, string, error)
	Latest(context.Context) (model.FailureMiningRun, []model.FailureCluster, []model.FailureImprovementProposal, bool, error)
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) ListSamples(ctx context.Context, since, until time.Time, limit int) ([]model.OnlineEvaluationSample, error) {
	if repository == nil || repository.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	if limit <= 0 || limit > MaximumSourceSamples {
		limit = MaximumSourceSamples
	}
	rows := make([]model.OnlineEvaluationSample, 0, limit)
	err := repository.db.WithContext(ctx).
		Where("simulation = ? AND created_at >= ? AND created_at <= ?", false, since.UTC(), until.UTC()).
		Order("created_at DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (repository *GormRepository) SaveSnapshot(ctx context.Context, snapshot MinedSnapshot) (bool, string, error) {
	if repository == nil || repository.db == nil {
		return false, "", gorm.ErrInvalidDB
	}
	if err := validateSnapshot(snapshot); err != nil {
		return false, "", err
	}
	created := false
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.FailureMiningRun
		result := tx.Where("input_hash = ?", snapshot.Run.InputHash).Limit(1).Find(&existing)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			snapshot.Run.ID = existing.ID
			return nil
		}
		if err := tx.Create(&snapshot.Run).Error; err != nil {
			return err
		}
		if len(snapshot.Clusters) > 0 {
			if err := tx.Create(&snapshot.Clusters).Error; err != nil {
				return err
			}
		}
		if len(snapshot.Proposals) > 0 {
			if err := tx.Create(&snapshot.Proposals).Error; err != nil {
				return err
			}
		}
		created = true
		return nil
	})
	if err != nil {
		var existing model.FailureMiningRun
		lookup := repository.db.WithContext(ctx).Where("input_hash = ?", snapshot.Run.InputHash).Limit(1).Find(&existing)
		if lookup.Error == nil && lookup.RowsAffected > 0 {
			return false, existing.ID, nil
		}
	}
	return created, snapshot.Run.ID, err
}

func (repository *GormRepository) Latest(ctx context.Context) (model.FailureMiningRun, []model.FailureCluster, []model.FailureImprovementProposal, bool, error) {
	if repository == nil || repository.db == nil {
		return model.FailureMiningRun{}, nil, nil, false, gorm.ErrInvalidDB
	}
	var run model.FailureMiningRun
	result := repository.db.WithContext(ctx).Order("created_at DESC, id DESC").Limit(1).Find(&run)
	if result.Error != nil {
		return model.FailureMiningRun{}, nil, nil, false, result.Error
	}
	if result.RowsAffected == 0 {
		return model.FailureMiningRun{}, []model.FailureCluster{}, []model.FailureImprovementProposal{}, false, nil
	}
	clusters := make([]model.FailureCluster, 0, run.ClusterCount)
	if err := repository.db.WithContext(ctx).Where("run_id = ?", run.ID).Order("sample_count DESC, id ASC").Find(&clusters).Error; err != nil {
		return model.FailureMiningRun{}, nil, nil, false, err
	}
	proposals := make([]model.FailureImprovementProposal, 0, run.ProposalCount)
	if err := repository.db.WithContext(ctx).Where("run_id = ?", run.ID).Order("id ASC").Find(&proposals).Error; err != nil {
		return model.FailureMiningRun{}, nil, nil, false, err
	}
	return run, clusters, proposals, true, nil
}

func validateSnapshot(snapshot MinedSnapshot) error {
	run := snapshot.Run
	if len(run.ID) != 64 || len(run.InputHash) != 64 || run.SchemaVersion != SchemaVersion || run.MinerVersion != MinerVersion || run.Status != StatusCompleted || run.SampleCount < run.EligibleCount || run.ClusterCount != len(snapshot.Clusters) || run.ProposalCount != len(snapshot.Proposals) || len(snapshot.Clusters) != len(snapshot.Proposals) {
		return errors.New("failure mining run is invalid")
	}
	clusters := make(map[string]struct{}, len(snapshot.Clusters))
	for _, cluster := range snapshot.Clusters {
		if len(cluster.ID) != 64 || cluster.RunID != run.ID || cluster.SchemaVersion != SchemaVersion || cluster.Status != StatusPendingReview || cluster.SampleCount <= 0 || len(cluster.SampleSetHash) != 64 {
			return errors.New("failure cluster is invalid")
		}
		clusters[cluster.ID] = struct{}{}
	}
	for _, proposal := range snapshot.Proposals {
		_, clusterExists := clusters[proposal.ClusterID]
		if len(proposal.ID) != 64 || len(proposal.ProposalHash) != 64 || proposal.ID != proposal.ProposalHash || proposal.RunID != run.ID || !clusterExists || proposal.SchemaVersion != SchemaVersion || proposal.State != StatusPendingReview || !proposal.RequiresHumanReview || proposal.OfflineGatePassed || proposal.IsolationCanaryPassed || proposal.Applied || !allowedCandidateKind(proposal.CandidateKind) {
			return errors.New("failure improvement proposal is invalid")
		}
	}
	return nil
}

func allowedCandidateKind(value string) bool {
	switch value {
	case "dataset", "prompt", "rule", "parameter":
		return true
	default:
		return false
	}
}
