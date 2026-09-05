package faultcampaign

import (
	"context"
	"sync"
	"testing"

	"GopherAI/model"
)

func TestBuildReportCoversThreeFailureClassesAndNeverApplies(t *testing.T) {
	report, err := BuildReport()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReport(report); err != nil {
		t.Fatal(err)
	}
	if report.Summary.DetectionRate != 1 || report.Summary.RecoveryRate != 1 || report.Summary.FalsePositiveRate != 0 || report.Summary.MeanMTTDSeconds != 60 || report.Summary.MeanRecoverySeconds != 180 {
		t.Fatalf("unexpected campaign summary: %+v", report.Summary)
	}
	wanted := map[string]bool{"rag_degradation": false, "agent_latency": false, "tool_failure": false}
	for _, scenario := range report.Scenarios {
		if _, ok := wanted[scenario.FaultClass]; !ok {
			t.Fatalf("unexpected fault class %q", scenario.FaultClass)
		}
		wanted[scenario.FaultClass] = true
		phases := []string{"before", "injected", "detected", "recommendation", "probe", "recovered"}
		for index, point := range scenario.Timeline {
			if point.Phase != phases[index] || point.Applied || point.TrafficChanged {
				t.Fatalf("unsafe timeline for %s: %+v", scenario.ScenarioID, point)
			}
		}
		if scenario.Timeline[3].WeightDeltaBasis != -1000 || scenario.Timeline[3].RecommendationAction != "reduce_candidate_weight" {
			t.Fatalf("unexpected recommendation for %s: %+v", scenario.ScenarioID, scenario.Timeline[3])
		}
	}
	for class, seen := range wanted {
		if !seen {
			t.Fatalf("missing fault class %s", class)
		}
	}
}

func TestBuildReportIsDeterministicAndTamperEvident(t *testing.T) {
	first, err := BuildReport()
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildReport()
	if err != nil {
		t.Fatal(err)
	}
	if first.ReportSHA256 != second.ReportSHA256 || first.CampaignID != second.CampaignID {
		t.Fatalf("report is not deterministic: %s %s", first.ReportSHA256, second.ReportSHA256)
	}
	first.Scenarios[0].Timeline[2].Applied = true
	if ValidateReport(first) == nil {
		t.Fatal("tampered applied state passed validation")
	}
}

type memoryRepository struct {
	mu      sync.Mutex
	records map[string]model.FaultInjectionCampaign
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{records: map[string]model.FaultInjectionCampaign{}}
}

func (repository *memoryRepository) Create(_ context.Context, record model.FaultInjectionCampaign) (bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.records[record.CampaignID]; exists {
		return false, nil
	}
	repository.records[record.CampaignID] = record
	return true, nil
}

func (repository *memoryRepository) Latest(context.Context) (int64, *model.FaultInjectionCampaign, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for _, record := range repository.records {
		copy := record
		return int64(len(repository.records)), &copy, nil
	}
	return 0, nil, nil
}

func TestServicePersistsOneImmutableCampaignPerFixtureVersion(t *testing.T) {
	repository := newMemoryRepository()
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.RunAcceptance(context.Background(), AcceptanceScenario)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.RunAcceptance(context.Background(), AcceptanceScenario)
	if err != nil {
		t.Fatal(err)
	}
	if first.CampaignID != second.CampaignID || len(repository.records) != 1 {
		t.Fatalf("campaign was not idempotent: %s %s rows=%d", first.CampaignID, second.CampaignID, len(repository.records))
	}
	audit, err := service.Audit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if audit.RunCount != 1 || audit.Latest == nil || audit.Latest.ReportSHA256 != first.ReportSHA256 {
		t.Fatalf("unexpected audit: %+v", audit)
	}
	if _, err := service.RunAcceptance(context.Background(), "production_outage"); err == nil {
		t.Fatal("unknown campaign scenario was accepted")
	}
}
