package faultcampaign

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"GopherAI/model"
)

type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, errors.New("fault campaign repository is required")
	}
	return &Service{repository: repository, now: time.Now}, nil
}

func (service *Service) RunAcceptance(ctx context.Context, scenario string) (CampaignReport, error) {
	if service == nil || service.repository == nil || strings.TrimSpace(scenario) != AcceptanceScenario {
		return CampaignReport{}, errors.New("unknown fault campaign scenario")
	}
	report, err := BuildReport()
	if err != nil {
		return CampaignReport{}, err
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return CampaignReport{}, err
	}
	record := model.FaultInjectionCampaign{CampaignID: report.CampaignID, SchemaVersion: report.SchemaVersion, FixtureVersion: report.FixtureVersion,
		Environment: report.Environment, Mode: report.Mode, ReportJSON: string(encoded), ReportSHA256: report.ReportSHA256,
		Simulation: true, Applied: false, CreatedAt: service.now().UTC()}
	if _, err := service.repository.Create(ctx, record); err != nil {
		return CampaignReport{}, err
	}
	return report, nil
}

func (service *Service) Audit(ctx context.Context) (AuditSnapshot, error) {
	if service == nil || service.repository == nil {
		return AuditSnapshot{}, errors.New("fault campaign service is unavailable")
	}
	count, record, err := service.repository.Latest(ctx)
	if err != nil {
		return AuditSnapshot{}, err
	}
	snapshot := AuditSnapshot{SchemaVersion: SchemaVersion, FixtureVersion: FixtureVersion, Environment: Environment, Mode: Mode, RunCount: count,
		Guardrails:  []string{"隔离 Fixture 不触碰生产依赖", "只复用生产检测算法", "建议不激活", "报告 Hash 可重放"},
		Limitations: []string{"尚未运行时 latest 为空。", "真实依赖停机与 Canary 必须使用独立基础设施。"}}
	if record == nil {
		return snapshot, nil
	}
	var report CampaignReport
	if err := json.Unmarshal([]byte(record.ReportJSON), &report); err != nil || ValidateReport(report) != nil {
		return AuditSnapshot{}, errors.New("stored fault campaign report is invalid")
	}
	snapshot.Latest = &report
	return snapshot, nil
}
