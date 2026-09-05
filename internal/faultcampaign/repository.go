package faultcampaign

import (
	"context"
	"encoding/json"
	"errors"

	"GopherAI/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository interface {
	Create(context.Context, model.FaultInjectionCampaign) (bool, error)
	Latest(context.Context) (int64, *model.FaultInjectionCampaign, error)
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) Create(ctx context.Context, record model.FaultInjectionCampaign) (bool, error) {
	if repository == nil || repository.db == nil {
		return false, gorm.ErrInvalidDB
	}
	if err := validateRecord(record); err != nil {
		return false, err
	}
	result := repository.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
	return result.RowsAffected == 1, result.Error
}

func (repository *GormRepository) Latest(ctx context.Context) (int64, *model.FaultInjectionCampaign, error) {
	if repository == nil || repository.db == nil {
		return 0, nil, gorm.ErrInvalidDB
	}
	var count int64
	if err := repository.db.WithContext(ctx).Model(&model.FaultInjectionCampaign{}).Count(&count).Error; err != nil {
		return 0, nil, err
	}
	var record model.FaultInjectionCampaign
	result := repository.db.WithContext(ctx).Order("created_at DESC, campaign_id DESC").Limit(1).Find(&record)
	if result.Error != nil {
		return 0, nil, result.Error
	}
	if result.RowsAffected == 0 {
		return count, nil, nil
	}
	if err := validateRecord(record); err != nil {
		return 0, nil, err
	}
	return count, &record, nil
}

func validateRecord(record model.FaultInjectionCampaign) error {
	if record.CampaignID == "" || record.SchemaVersion != SchemaVersion || record.FixtureVersion != FixtureVersion || record.Environment != Environment ||
		record.Mode != Mode || !record.Simulation || record.Applied || record.ReportSHA256 == "" || !json.Valid([]byte(record.ReportJSON)) || record.CreatedAt.IsZero() {
		return errors.New("fault injection campaign record is invalid")
	}
	var report CampaignReport
	if err := json.Unmarshal([]byte(record.ReportJSON), &report); err != nil || report.CampaignID != record.CampaignID || report.ReportSHA256 != record.ReportSHA256 {
		return errors.New("fault injection campaign report identity mismatch")
	}
	return ValidateReport(report)
}
