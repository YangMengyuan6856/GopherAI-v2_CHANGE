package observability

import (
	"testing"
	"time"
)

func TestAnomalyDetectorUsesFixedAndCurrentExcludedZScore(t *testing.T) {
	policy, observations, err := AcceptanceAnomalyScenario("quality_drop", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	result, err := AnalyzeMetricWindow(policy, observations)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Anomalous || !result.Fixed.Anomalous || !result.ZScore.Anomalous || !result.ZScore.CurrentExcluded || result.ZScore.BaselinePoints != 30 {
		t.Fatalf("quality drop not detected by both detectors: %+v", result)
	}
	if result.Recommendation.Mode != "recommend_only" || result.Recommendation.Applied || result.Recommendation.WeightDeltaBasis != -1000 || result.Recommendation.IncidentKey == "" {
		t.Fatalf("unsafe recommendation: %+v", result.Recommendation)
	}
}

func TestAnomalyDetectorSuppressesLowPopulation(t *testing.T) {
	policy, observations, _ := AcceptanceAnomalyScenario("low_sample", time.Now())
	result, err := AnalyzeMetricWindow(policy, observations)
	if err != nil {
		t.Fatal(err)
	}
	if result.Anomalous || result.DecisionStatus != "insufficient_data" || result.Fixed.Status != "suppressed" || result.Fixed.Population != 12 || result.ZScore.ReasonCode != "minimum_population_not_met" || result.Recommendation.Action != "none" {
		t.Fatalf("low-population anomaly was not suppressed: %+v", result)
	}
}

func TestAnomalyDetectorDoesNotCallSinglePointHealthy(t *testing.T) {
	policy, _, _ := AcceptanceAnomalyScenario("healthy", time.Now())
	result, err := AnalyzeMetricWindow(policy, []MetricObservation{{ObservedAt: time.Now().UTC(), Value: .70, Population: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if result.DecisionStatus != "insufficient_window" || result.Fixed.Status != "suppressed" || result.Recommendation.Action != "none" || result.Recommendation.Applied {
		t.Fatalf("single point must remain undecided: %+v", result)
	}
}

func TestAnomalyDetectorHandlesZeroVarianceAdverseShift(t *testing.T) {
	policy, observations, _ := AcceptanceAnomalyScenario("zero_variance_shift", time.Now())
	result, err := AnalyzeMetricWindow(policy, observations)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ZScore.Anomalous || !result.ZScore.ZeroVariance || result.ZScore.BaselineStdDev > 1e-12 || result.ZScore.ReasonCode != "zero_variance_adverse_shift" {
		t.Fatalf("zero-variance shift not handled safely: %+v", result.ZScore)
	}
}

func TestAnomalyDetectorIgnoresFloatingNoiseInZeroVarianceWindow(t *testing.T) {
	policy := DetectionPolicy{Metric: "request_p95_latency_seconds", Strategy: "diagnosis_collaborative", Direction: DirectionLowerIsBetter, WarningThreshold: 2, CriticalThreshold: 4, MinimumPopulation: 50, WindowSize: 30, MinimumWindow: 10, ZScoreThreshold: 3, ConsecutivePoints: 2}
	now := time.Now().UTC()
	observations := make([]MetricObservation, 0, 12)
	for index := 0; index < 12; index++ {
		observations = append(observations, MetricObservation{ObservedAt: now.Add(time.Duration(index) * time.Minute), Value: 1.1, Population: 100})
	}
	result, err := AnalyzeMetricWindow(policy, observations)
	if err != nil {
		t.Fatal(err)
	}
	if result.Anomalous || result.DecisionStatus != "healthy" || result.ZScore.ZeroVariance {
		t.Fatalf("floating-point noise must not become a zero-variance anomaly: %+v", result)
	}
}

func TestIncidentTrackerDeduplicatesAndEmitsRecovery(t *testing.T) {
	tracker := NewIncidentTracker()
	if event := tracker.Apply("incident-1", true); event.Type != "triggered" || !event.Notify {
		t.Fatalf("unexpected trigger: %+v", event)
	}
	if event := tracker.Apply("incident-1", true); event.Type != "duplicate_suppressed" || event.Notify {
		t.Fatalf("unexpected duplicate: %+v", event)
	}
	if event := tracker.Apply("incident-1", false); event.Type != "recovered" || !event.Notify {
		t.Fatalf("unexpected recovery: %+v", event)
	}
	if event := tracker.Apply("incident-1", false); event.Type != "healthy" || event.Notify {
		t.Fatalf("unexpected healthy event: %+v", event)
	}
}
