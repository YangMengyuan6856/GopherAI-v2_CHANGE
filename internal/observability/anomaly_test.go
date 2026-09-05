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
	if result.Anomalous || result.Fixed.Status != "suppressed" || result.ZScore.ReasonCode != "minimum_population_not_met" || result.Recommendation.Action != "none" {
		t.Fatalf("low-population anomaly was not suppressed: %+v", result)
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
