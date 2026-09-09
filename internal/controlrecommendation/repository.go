package controlrecommendation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"GopherAI/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) Create(ctx context.Context, record model.ControlRecommendation) (bool, error) {
	if repository == nil || repository.db == nil {
		return false, gorm.ErrInvalidDB
	}
	if err := validateRecord(record); err != nil {
		return false, err
	}
	result := repository.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (repository *GormRepository) Audit(ctx context.Context, limit int) (int64, int64, []model.ControlRecommendation, error) {
	if repository == nil || repository.db == nil || limit < 1 || limit > 50 {
		return 0, 0, nil, gorm.ErrInvalidDB
	}
	var recommended, blocked int64
	if err := repository.db.WithContext(ctx).Model(&model.ControlRecommendation{}).Where("status = ?", StatusRecommended).Count(&recommended).Error; err != nil {
		return 0, 0, nil, err
	}
	if err := repository.db.WithContext(ctx).Model(&model.ControlRecommendation{}).Where("status = ?", StatusBlocked).Count(&blocked).Error; err != nil {
		return 0, 0, nil, err
	}
	var rows []model.ControlRecommendation
	if err := repository.db.WithContext(ctx).Order("created_at DESC, recommendation_id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return 0, 0, nil, err
	}
	return recommended, blocked, rows, nil
}

func validateRecord(record model.ControlRecommendation) error {
	if len(record.RecommendationID) != 64 || record.Mode != ModeRecommendOnly || (record.Status != StatusRecommended && record.Status != StatusBlocked) ||
		strings.TrimSpace(record.Source) == "" || strings.TrimSpace(record.IncidentKey) == "" || len(record.BatchID) != 64 ||
		strings.TrimSpace(record.Metric) == "" || strings.TrimSpace(record.Strategy) == "" || strings.TrimSpace(record.ParentPolicyVersion) == "" ||
		len(record.ParentPolicySHA256) != 64 || strings.TrimSpace(record.ReasonCode) == "" || len(record.EvidenceSHA256) != 64 ||
		strings.TrimSpace(record.DetectorVersion) == "" || strings.TrimSpace(record.RulesVersion) == "" || len(record.RulesSHA256) != 64 ||
		record.Applied || record.CreatedAt.IsZero() || !json.Valid([]byte(record.EvidenceJSON)) {
		return errors.New("control recommendation record is invalid")
	}
	evidenceDigest := sha256.Sum256([]byte(record.EvidenceJSON))
	if hex.EncodeToString(evidenceDigest[:]) != record.EvidenceSHA256 {
		return errors.New("control recommendation evidence hash mismatch")
	}
	if record.Status == StatusRecommended {
		if record.ReasonCode != ReasonCandidateCreated || record.Intent == "" || record.WeightDeltaBasis >= 0 || record.WeightDeltaBasis < -maximumWeightStepBasis ||
			record.ProposedWeightBasis-record.BeforeWeightBasis != record.WeightDeltaBasis || record.FallbackStrategy == "" ||
			record.FallbackProposedBasis-record.FallbackBeforeBasis != -record.WeightDeltaBasis || record.CandidatePolicyVersion == "" ||
			len(record.CandidatePolicySHA256) != 64 || !json.Valid([]byte(record.CandidatePolicyJSON)) {
			return errors.New("recommended policy candidate is invalid")
		}
		candidateDigest := sha256.Sum256([]byte(record.CandidatePolicyJSON))
		if hex.EncodeToString(candidateDigest[:]) != record.CandidatePolicySHA256 {
			return errors.New("candidate policy hash mismatch")
		}
	} else if record.CandidatePolicyVersion != "" || record.CandidatePolicySHA256 != "" || record.CandidatePolicyJSON != "" || record.WeightDeltaBasis != 0 {
		return errors.New("blocked recommendation cannot carry a policy candidate")
	}
	return nil
}
