package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const AnomalyDetectorVersion = "bounded-anomaly-detector-v1"

const (
	DirectionHigherIsBetter = "higher_is_better"
	DirectionLowerIsBetter  = "lower_is_better"
)

type DetectionPolicy struct {
	Metric            string  `json:"metric"`
	Strategy          string  `json:"strategy"`
	Direction         string  `json:"direction"`
	WarningThreshold  float64 `json:"warning_threshold"`
	CriticalThreshold float64 `json:"critical_threshold"`
	MinimumPopulation int     `json:"minimum_population"`
	WindowSize        int     `json:"window_size"`
	MinimumWindow     int     `json:"minimum_window"`
	ZScoreThreshold   float64 `json:"z_score_threshold"`
	ConsecutivePoints int     `json:"consecutive_points"`
}

type MetricObservation struct {
	ObservedAt time.Time `json:"observed_at"`
	Value      float64   `json:"value"`
	Population int       `json:"population"`
}

type FixedThresholdSignal struct {
	Status      string  `json:"status"`
	ReasonCode  string  `json:"reason_code"`
	Observed    float64 `json:"observed"`
	Population  int     `json:"population"`
	BreachCount int     `json:"breach_count"`
	Anomalous   bool    `json:"anomalous"`
}

type ZScoreSignal struct {
	Status          string  `json:"status"`
	ReasonCode      string  `json:"reason_code"`
	BaselinePoints  int     `json:"baseline_points"`
	BaselineMean    float64 `json:"baseline_mean"`
	BaselineStdDev  float64 `json:"baseline_stddev"`
	AdverseZScore   float64 `json:"adverse_z_score"`
	ZeroVariance    bool    `json:"zero_variance"`
	CandidatePoints int     `json:"candidate_points"`
	BreachCount     int     `json:"breach_count"`
	CurrentExcluded bool    `json:"current_excluded"`
	Anomalous       bool    `json:"anomalous"`
}

type AnomalyRecommendation struct {
	Mode             string `json:"mode"`
	Action           string `json:"action"`
	WeightDeltaBasis int    `json:"weight_delta_basis"`
	Applied          bool   `json:"applied"`
	ReasonCode       string `json:"reason_code"`
	IncidentKey      string `json:"incident_key,omitempty"`
}

type AnomalyAnalysis struct {
	DetectorVersion string                `json:"detector_version"`
	Policy          DetectionPolicy       `json:"policy"`
	PointCount      int                   `json:"point_count"`
	DecisionStatus  string                `json:"decision_status"`
	Fixed           FixedThresholdSignal  `json:"fixed_threshold"`
	ZScore          ZScoreSignal          `json:"z_score"`
	Anomalous       bool                  `json:"anomalous"`
	Recommendation  AnomalyRecommendation `json:"recommendation"`
	Guardrails      []string              `json:"guardrails"`
}

func AnalyzeMetricWindow(policy DetectionPolicy, observations []MetricObservation) (AnomalyAnalysis, error) {
	if err := validateDetectionPolicy(policy); err != nil {
		return AnomalyAnalysis{}, err
	}
	if len(observations) == 0 {
		return AnomalyAnalysis{}, errors.New("metric observations are required")
	}
	ordered := append([]MetricObservation(nil), observations...)
	sort.SliceStable(ordered, func(left, right int) bool { return ordered[left].ObservedAt.Before(ordered[right].ObservedAt) })
	latest := ordered[len(ordered)-1]
	analysis := AnomalyAnalysis{
		DetectorVersion: AnomalyDetectorVersion, Policy: policy, PointCount: len(ordered), DecisionStatus: "healthy",
		Fixed:          FixedThresholdSignal{Status: "healthy", ReasonCode: "within_fixed_threshold", Observed: latest.Value, Population: latest.Population},
		ZScore:         ZScoreSignal{Status: "insufficient_window", ReasonCode: "minimum_window_not_met", CurrentExcluded: true},
		Recommendation: AnomalyRecommendation{Mode: "recommend_only", Action: "none", Applied: false, ReasonCode: "no_anomaly"},
		Guardrails:     []string{"不会写入 active policy", "不会自动切流", "不会执行修复工具", "建议必须经过离线评测与人工审核"},
	}
	if latest.Population < policy.MinimumPopulation {
		analysis.DecisionStatus = "insufficient_data"
		analysis.Fixed.Status, analysis.Fixed.ReasonCode = "suppressed", "minimum_population_not_met"
		analysis.ZScore.Status, analysis.ZScore.ReasonCode = "suppressed", "minimum_population_not_met"
		analysis.Recommendation.ReasonCode = "insufficient_population"
		return analysis, nil
	}
	tailCount := policy.ConsecutivePoints
	if tailCount > len(ordered) {
		tailCount = len(ordered)
	}
	tail := ordered[len(ordered)-tailCount:]
	for _, point := range tail {
		if point.Population < policy.MinimumPopulation {
			analysis.DecisionStatus = "insufficient_data"
			analysis.Fixed.Status, analysis.Fixed.ReasonCode = "suppressed", "minimum_population_not_met"
			analysis.ZScore.Status, analysis.ZScore.ReasonCode = "suppressed", "minimum_population_not_met"
			analysis.Recommendation.ReasonCode = "insufficient_population"
			return analysis, nil
		}
		if thresholdBreached(point.Value, policy.Direction, policy.WarningThreshold) {
			analysis.Fixed.BreachCount++
		}
	}
	if analysis.Fixed.BreachCount >= policy.ConsecutivePoints {
		analysis.Fixed.Anomalous = true
		if thresholdBreached(latest.Value, policy.Direction, policy.CriticalThreshold) {
			analysis.Fixed.Status, analysis.Fixed.ReasonCode = "critical", "critical_threshold_breached"
		} else {
			analysis.Fixed.Status, analysis.Fixed.ReasonCode = "warning", "warning_threshold_breached"
		}
	}
	baselineEnd := len(ordered) - policy.ConsecutivePoints
	if baselineEnd >= policy.MinimumWindow {
		baselineStart := baselineEnd - policy.WindowSize
		if baselineStart < 0 {
			baselineStart = 0
		}
		baseline := ordered[baselineStart:baselineEnd]
		analysis.ZScore.BaselinePoints = len(baseline)
		analysis.ZScore.CandidatePoints = len(tail)
		mean, stddev := meanStdDev(baseline)
		analysis.ZScore.BaselineMean, analysis.ZScore.BaselineStdDev = mean, stddev
		allBreached := true
		latestZ := 0.0
		zeroVarianceShift := false
		for _, point := range tail {
			adverse, shifted := adverseZScore(point.Value, mean, stddev, policy.Direction)
			latestZ, zeroVarianceShift = adverse, zeroVarianceShift || shifted
			if shifted || adverse >= policy.ZScoreThreshold {
				analysis.ZScore.BreachCount++
			} else {
				allBreached = false
			}
		}
		analysis.ZScore.AdverseZScore = latestZ
		analysis.ZScore.ZeroVariance = zeroVarianceShift
		analysis.ZScore.Anomalous = allBreached && analysis.ZScore.BreachCount >= policy.ConsecutivePoints
		if analysis.ZScore.Anomalous {
			analysis.ZScore.Status = "anomalous"
			if zeroVarianceShift {
				analysis.ZScore.ReasonCode = "zero_variance_adverse_shift"
			} else {
				analysis.ZScore.ReasonCode = "z_score_threshold_breached"
			}
		} else {
			analysis.ZScore.Status, analysis.ZScore.ReasonCode = "healthy", "within_z_score_threshold"
		}
	}
	analysis.Anomalous = analysis.Fixed.Anomalous || analysis.ZScore.Anomalous
	if analysis.Anomalous {
		analysis.DecisionStatus = "anomalous"
		analysis.Recommendation = AnomalyRecommendation{
			Mode: "recommend_only", Action: "reduce_candidate_weight", WeightDeltaBasis: -1000, Applied: false,
			ReasonCode: "bounded_anomaly_detected", IncidentKey: anomalyIncidentKey(policy),
		}
	}
	return analysis, nil
}

type IncidentEvent struct {
	Type        string `json:"type"`
	IncidentKey string `json:"incident_key"`
	Notify      bool   `json:"notify"`
}

type IncidentTracker struct {
	mu     sync.Mutex
	active map[string]bool
}

func NewIncidentTracker() *IncidentTracker { return &IncidentTracker{active: map[string]bool{}} }

func (tracker *IncidentTracker) Apply(incidentKey string, anomalous bool) IncidentEvent {
	incidentKey = strings.TrimSpace(incidentKey)
	if tracker == nil || incidentKey == "" {
		return IncidentEvent{Type: "ignored", IncidentKey: incidentKey}
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	wasActive := tracker.active[incidentKey]
	if anomalous {
		tracker.active[incidentKey] = true
		if wasActive {
			return IncidentEvent{Type: "duplicate_suppressed", IncidentKey: incidentKey}
		}
		return IncidentEvent{Type: "triggered", IncidentKey: incidentKey, Notify: true}
	}
	if wasActive {
		delete(tracker.active, incidentKey)
		return IncidentEvent{Type: "recovered", IncidentKey: incidentKey, Notify: true}
	}
	return IncidentEvent{Type: "healthy", IncidentKey: incidentKey}
}

func AcceptanceAnomalyScenario(name string, now time.Time) (DetectionPolicy, []MetricObservation, error) {
	name = strings.TrimSpace(name)
	policy := DetectionPolicy{Metric: "rag_grounded_answer_rate", Strategy: "rag_deep", Direction: DirectionHigherIsBetter, WarningThreshold: .93, CriticalThreshold: .90, MinimumPopulation: 50, WindowSize: 30, MinimumWindow: 10, ZScoreThreshold: 3, ConsecutivePoints: 2}
	base, tail, population := .965, []float64{.962, .964}, 100
	switch name {
	case "healthy":
	case "quality_drop":
		tail = []float64{.72, .70}
	case "low_sample":
		tail, population = []float64{.72, .70}, 12
	case "zero_variance_shift":
		base, tail = .97, []float64{.70, .70}
	case "latency_spike":
		policy = DetectionPolicy{Metric: "request_p95_latency_seconds", Strategy: "diagnosis_collaborative", Direction: DirectionLowerIsBetter, WarningThreshold: 2, CriticalThreshold: 4, MinimumPopulation: 50, WindowSize: 30, MinimumWindow: 10, ZScoreThreshold: 3, ConsecutivePoints: 2}
		base, tail = 1.05, []float64{4.8, 5.2}
	default:
		return DetectionPolicy{}, nil, errors.New("unknown anomaly acceptance scenario")
	}
	observations := make([]MetricObservation, 0, 32)
	for index := 0; index < 30; index++ {
		value := base
		if name != "zero_variance_shift" {
			value += float64(index%5-2) * .002
		}
		observations = append(observations, MetricObservation{ObservedAt: now.Add(time.Duration(index-32) * time.Minute), Value: value, Population: 100})
	}
	for index, value := range tail {
		observations = append(observations, MetricObservation{ObservedAt: now.Add(time.Duration(index-2) * time.Minute), Value: value, Population: population})
	}
	return policy, observations, nil
}

func validateDetectionPolicy(policy DetectionPolicy) error {
	if policy.Metric == "" || policy.Strategy == "" || (policy.Direction != DirectionHigherIsBetter && policy.Direction != DirectionLowerIsBetter) || policy.MinimumPopulation < 1 || policy.WindowSize < 2 || policy.MinimumWindow < 2 || policy.MinimumWindow > policy.WindowSize || policy.ZScoreThreshold <= 0 || policy.ConsecutivePoints < 1 {
		return errors.New("detection policy is invalid")
	}
	if policy.Direction == DirectionHigherIsBetter && policy.CriticalThreshold > policy.WarningThreshold {
		return errors.New("higher-is-better thresholds are inverted")
	}
	if policy.Direction == DirectionLowerIsBetter && policy.CriticalThreshold < policy.WarningThreshold {
		return errors.New("lower-is-better thresholds are inverted")
	}
	return nil
}

func thresholdBreached(value float64, direction string, threshold float64) bool {
	if direction == DirectionHigherIsBetter {
		return value < threshold
	}
	return value > threshold
}

func meanStdDev(observations []MetricObservation) (float64, float64) {
	if len(observations) == 0 {
		return 0, 0
	}
	mean := 0.0
	for _, point := range observations {
		mean += point.Value
	}
	mean /= float64(len(observations))
	variance := 0.0
	for _, point := range observations {
		delta := point.Value - mean
		variance += delta * delta
	}
	variance /= float64(len(observations))
	return mean, math.Sqrt(variance)
}

func adverseZScore(value float64, mean float64, stddev float64, direction string) (float64, bool) {
	delta := value - mean
	if direction == DirectionHigherIsBetter {
		delta = mean - value
	}
	if delta <= 0 {
		return 0, false
	}
	if stddev <= 1e-12 {
		return 0, true
	}
	return delta / stddev, false
}

func anomalyIncidentKey(policy DetectionPolicy) string {
	digest := sha256.Sum256([]byte(AnomalyDetectorVersion + "|" + policy.Metric + "|" + policy.Strategy))
	return "incident-" + hex.EncodeToString(digest[:])[:16]
}
