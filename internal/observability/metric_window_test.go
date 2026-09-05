package observability

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"GopherAI/model"
)

type fakeMetricWindowSource struct {
	runtime PrometheusRuntimeSnapshot
	samples map[FixedPrometheusMetric]PrometheusInstantSample
	errKey  FixedPrometheusMetric
}

func (source *fakeMetricWindowSource) Snapshot(context.Context) (PrometheusRuntimeSnapshot, error) {
	return source.runtime, nil
}

func (source *fakeMetricWindowSource) QueryFixedMetric(_ context.Context, metric FixedPrometheusMetric) (PrometheusInstantSample, error) {
	if metric == source.errKey {
		return PrometheusInstantSample{}, errors.New("private upstream detail")
	}
	return source.samples[metric], nil
}

type fakeMetricWindowRepository struct {
	stored        []model.MetricWindowSnapshot
	latest        []model.MetricWindowSnapshot
	history       map[string][]model.MetricWindowSnapshot
	historyFilter []string
}

func (repository *fakeMetricWindowRepository) StoreBatch(_ context.Context, records []model.MetricWindowSnapshot) error {
	repository.stored = append([]model.MetricWindowSnapshot(nil), records...)
	return nil
}

func (repository *fakeMetricWindowRepository) LatestBatch(context.Context) ([]model.MetricWindowSnapshot, error) {
	if repository.latest == nil {
		return nil, errors.New("no batch")
	}
	return append([]model.MetricWindowSnapshot(nil), repository.latest...), nil
}

func (repository *fakeMetricWindowRepository) RecentObserved(_ context.Context, metric string, strategy string, rulesVersion string, rulesSHA256 string, _ int) ([]model.MetricWindowSnapshot, error) {
	repository.historyFilter = []string{metric, strategy, rulesVersion, rulesSHA256}
	return append([]model.MetricWindowSnapshot(nil), repository.history[metric+"|"+strategy]...), nil
}

func TestMetricWindowCapturePersistsCompleteFixedBatch(t *testing.T) {
	now := time.Date(2026, 9, 6, 2, 3, 41, 0, time.UTC)
	source := readyMetricWindowSource(now)
	repository := &fakeMetricWindowRepository{}
	service := NewMetricWindowService(source, repository)
	service.clock = func() time.Time { return now }
	batch, err := service.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != "observed" || len(batch.Points) != 2 || len(repository.stored) != 2 || batch.CollectedAt.Second() != 0 || len(batch.BatchID) != 64 {
		t.Fatalf("unexpected persisted batch: %+v stored=%+v", batch, repository.stored)
	}
	for _, record := range repository.stored {
		if record.Collector != MetricWindowCollectorVersion || record.DataStatus != MetricWindowObserved || record.Population != 80 || len(record.SnapshotKey) != 64 {
			t.Fatalf("unexpected record: %+v", record)
		}
	}
}

func TestMetricWindowCaptureRejectsValueWithoutPopulationAtomically(t *testing.T) {
	now := time.Now().UTC()
	source := readyMetricWindowSource(now)
	source.samples[PrometheusRAGDeepPopulation] = PrometheusInstantSample{Status: PrometheusMetricNoSeries}
	repository := &fakeMetricWindowRepository{}
	service := NewMetricWindowService(source, repository)
	_, err := service.Capture(context.Background())
	if err == nil || len(repository.stored) != 0 || strings.Contains(err.Error(), "private") {
		t.Fatalf("partial batch must fail closed without upstream detail: err=%v stored=%d", err, len(repository.stored))
	}
}

func TestMetricWindowCapturePersistsExplicitWarmingState(t *testing.T) {
	now := time.Now().UTC()
	source := readyMetricWindowSource(now)
	source.samples[PrometheusCollaborativeRequestP95] = PrometheusInstantSample{Status: PrometheusMetricNonFinite, ObservedAt: now}
	repository := &fakeMetricWindowRepository{}
	service := NewMetricWindowService(source, repository)
	batch, err := service.Capture(context.Background())
	if err != nil || batch.Status != "warming" || batch.Points[1].DataStatus != MetricWindowNoFiniteValue || len(repository.stored) != 2 {
		t.Fatalf("non-finite no-traffic signal must be explicit and durable: %+v err=%v", batch, err)
	}
}

func TestProductionAnalysisUsesOnlyMatchingRuleProvenance(t *testing.T) {
	now := time.Date(2026, 9, 6, 3, 0, 0, 0, time.UTC)
	rulesSHA := strings.Repeat("b", 64)
	repository := &fakeMetricWindowRepository{history: map[string][]model.MetricWindowSnapshot{}}
	for _, spec := range ProductionMetricWindowSpecs() {
		record := model.MetricWindowSnapshot{BatchID: strings.Repeat("c", 64), Metric: spec.Metric, Strategy: spec.Strategy, DataStatus: MetricWindowObserved, Value: .97, Population: 100, WindowSeconds: spec.WindowSeconds, ObservedAt: now, CollectedAt: now, RulesVersion: RecordingRulesVersion, RulesSHA256: rulesSHA}
		if spec.Policy.Direction == DirectionLowerIsBetter {
			record.Value = 1.1
		}
		repository.latest = append(repository.latest, record)
		for index := 0; index < 12; index++ {
			point := record
			point.ObservedAt = now.Add(time.Duration(index-12) * time.Minute)
			repository.history[spec.Metric+"|"+spec.Strategy] = append(repository.history[spec.Metric+"|"+spec.Strategy], point)
		}
	}
	service := NewMetricWindowService(nil, repository)
	result, err := service.LatestProductionAnalysis(context.Background())
	if err != nil || result.Status != "ready" || result.Simulation || len(result.Series) != 2 {
		t.Fatalf("unexpected production analysis: %+v err=%v", result, err)
	}
	if len(repository.historyFilter) != 4 || repository.historyFilter[2] != RecordingRulesVersion || repository.historyFilter[3] != rulesSHA {
		t.Fatalf("history did not bind rule provenance: %v", repository.historyFilter)
	}
	for _, series := range result.Series {
		if series.Analysis == nil || series.Analysis.Recommendation.Applied || series.Analysis.Recommendation.Mode != "recommend_only" {
			t.Fatalf("production analysis crossed recommend-only boundary: %+v", series)
		}
	}
}

func readyMetricWindowSource(now time.Time) *fakeMetricWindowSource {
	observed := func(value float64) PrometheusInstantSample {
		return PrometheusInstantSample{Status: PrometheusMetricObserved, Value: value, ObservedAt: now}
	}
	return &fakeMetricWindowSource{
		runtime: PrometheusRuntimeSnapshot{Status: "ready", RulesVersion: RecordingRulesVersion, RulesSHA256: strings.Repeat("a", 64)},
		samples: map[FixedPrometheusMetric]PrometheusInstantSample{
			PrometheusRAGDeepGroundedRate:     observed(.97),
			PrometheusRAGDeepPopulation:       observed(80),
			PrometheusCollaborativeRequestP95: observed(1.2),
			PrometheusCollaborativePopulation: observed(80),
		},
	}
}
