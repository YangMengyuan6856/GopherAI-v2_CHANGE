package evaluation

import (
	"GopherAI/internal/intent"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

type PatternLabelMetrics struct {
	Support         int     `json:"support"`
	Matched         int     `json:"matched"`
	Correct         int     `json:"correct"`
	Coverage        float64 `json:"coverage"`
	SelectiveRecall float64 `json:"selective_recall"`
}

type PatternFailure struct {
	ID             string   `json:"id"`
	Expected       string   `json:"expected"`
	Predicted      string   `json:"predicted"`
	Confidence     float64  `json:"confidence"`
	Matched        bool     `json:"matched"`
	IsCompound     bool     `json:"is_compound"`
	ReasonCodes    []string `json:"reason_codes"`
	SevereMisroute bool     `json:"severe_misroute"`
}

type IntentPatternReport struct {
	SchemaVersion          string                         `json:"schema_version"`
	DatasetVersion         string                         `json:"dataset_version"`
	RubricVersion          string                         `json:"rubric_version"`
	RecognizerVersion      string                         `json:"recognizer_version"`
	CandidateVersion       string                         `json:"candidate_version"`
	GeneratedAt            time.Time                      `json:"generated_at"`
	CaseCount              int                            `json:"case_count"`
	HumanReviewed          bool                           `json:"human_reviewed"`
	BaselineEligible       bool                           `json:"baseline_eligible"`
	MatchedCount           int                            `json:"matched_count"`
	CorrectMatchedCount    int                            `json:"correct_matched_count"`
	ConflictCount          int                            `json:"conflict_count"`
	Coverage               float64                        `json:"coverage"`
	SelectiveAccuracy      float64                        `json:"selective_accuracy"`
	FalseShortCircuitRate  float64                        `json:"false_short_circuit_rate"`
	SevereShortCircuitRate float64                        `json:"severe_short_circuit_rate"`
	P95LatencyMicros       int64                          `json:"p95_latency_micros"`
	PatternGatePassed      bool                           `json:"pattern_gate_passed"`
	G4Evaluated            bool                           `json:"g4_evaluated"`
	LabelMetrics           map[string]PatternLabelMetrics `json:"label_metrics"`
	Confusion              map[string]map[string]int      `json:"matched_confusion"`
	ReasonContributions    map[string]int                 `json:"reason_contributions"`
	Failures               []PatternFailure               `json:"failures"`
}

func EvaluatePattern(cases []IntentCase, candidateVersion string, now time.Time) IntentPatternReport {
	recognizer := intent.NewPatternRecognizer()
	report := IntentPatternReport{
		SchemaVersion: "1", DatasetVersion: IntentDatasetVersion, RubricVersion: intent.RubricVersion,
		RecognizerVersion: intent.PatternVersion, CandidateVersion: strings.TrimSpace(candidateVersion), GeneratedAt: now.UTC(),
		CaseCount: len(cases), HumanReviewed: true, LabelMetrics: make(map[string]PatternLabelMetrics, 6),
		Confusion: make(map[string]map[string]int, 6), ReasonContributions: make(map[string]int), Failures: make([]PatternFailure, 0),
	}
	latencies := make([]int64, 0, len(cases))
	for _, item := range cases {
		if item.ReviewedBy != "human" {
			report.HumanReviewed = false
		}
		labelMetric := report.LabelMetrics[item.Expected.Intent]
		labelMetric.Support++
		started := time.Now()
		decision := recognizer.Recognize(intent.PatternInput{Question: item.Question, PreviousIntent: item.Context.PreviousIntent})
		latencies = append(latencies, time.Since(started).Nanoseconds())
		for _, reason := range decision.ReasonCodes {
			report.ReasonContributions[reason]++
		}
		if decision.Result.IsCompound && !decision.Matched {
			report.ConflictCount++
		}
		if !decision.Matched {
			report.Failures = append(report.Failures, PatternFailure{ID: item.ID, Expected: item.Expected.Intent, Predicted: decision.Result.Intent,
				Confidence: decision.Result.Confidence, Matched: false, IsCompound: decision.Result.IsCompound, ReasonCodes: decision.ReasonCodes})
			report.LabelMetrics[item.Expected.Intent] = labelMetric
			continue
		}
		report.MatchedCount++
		labelMetric.Matched++
		if report.Confusion[item.Expected.Intent] == nil {
			report.Confusion[item.Expected.Intent] = make(map[string]int, 6)
		}
		report.Confusion[item.Expected.Intent][decision.Result.Intent]++
		if decision.Result.Intent == item.Expected.Intent {
			report.CorrectMatchedCount++
			labelMetric.Correct++
		} else {
			severe := intent.IsSevereMisroute(item.Expected.Intent, decision.Result.Intent)
			report.Failures = append(report.Failures, PatternFailure{ID: item.ID, Expected: item.Expected.Intent, Predicted: decision.Result.Intent,
				Confidence: decision.Result.Confidence, Matched: true, IsCompound: decision.Result.IsCompound, ReasonCodes: decision.ReasonCodes, SevereMisroute: severe})
		}
		report.LabelMetrics[item.Expected.Intent] = labelMetric
	}

	severe, falseShortCircuit := 0, 0
	for _, failure := range report.Failures {
		if !failure.Matched {
			continue
		}
		falseShortCircuit++
		if failure.SevereMisroute {
			severe++
		}
	}
	if report.CaseCount > 0 {
		report.Coverage = float64(report.MatchedCount) / float64(report.CaseCount)
	}
	if report.MatchedCount > 0 {
		report.SelectiveAccuracy = float64(report.CorrectMatchedCount) / float64(report.MatchedCount)
		report.FalseShortCircuitRate = float64(falseShortCircuit) / float64(report.MatchedCount)
		report.SevereShortCircuitRate = float64(severe) / float64(report.MatchedCount)
	}
	for _, label := range intent.Labels() {
		metric := report.LabelMetrics[label]
		if metric.Support > 0 {
			metric.Coverage = float64(metric.Matched) / float64(metric.Support)
			metric.SelectiveRecall = float64(metric.Correct) / float64(metric.Support)
		}
		report.LabelMetrics[label] = metric
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	if len(latencies) > 0 {
		index := (95*len(latencies)+99)/100 - 1
		if index < 0 {
			index = 0
		}
		report.P95LatencyMicros = (latencies[index] + int64(time.Microsecond) - 1) / int64(time.Microsecond)
		if report.P95LatencyMicros == 0 {
			report.P95LatencyMicros = 1
		}
	}
	report.PatternGatePassed = report.MatchedCount > 0 && report.SelectiveAccuracy >= 0.95 && report.SevereShortCircuitRate == 0
	report.BaselineEligible = report.HumanReviewed && report.G4Evaluated
	return report
}

func WriteIntentPatternJSON(writer io.Writer, report IntentPatternReport) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func WriteIntentPatternMarkdown(writer io.Writer, report IntentPatternReport) error {
	if writer == nil {
		return fmt.Errorf("report writer is required")
	}
	_, err := fmt.Fprintf(writer, `# Intent Pattern Evaluation

- Dataset: %s (%d cases)
- Rubric: %s
- Recognizer: %s
- Candidate: %s
- Human reviewed: %t
- Baseline eligible: %t

## Pattern-stage metrics

| Metric | Result |
|---|---:|
| Coverage | %.4f |
| Selective accuracy | %.4f |
| False short-circuit rate | %.4f |
| Severe short-circuit rate | %.4f |
| Conflicts passed to later stages | %d |
| P95 pattern latency | %d µs |
| Pattern gate passed | %t |
| Full G4 evaluated | %t |

This is a selective stage report, not end-to-end intent accuracy. Abstentions
must proceed to Prototype/LLM fusion; counting them as general would hide
missing coverage. G4 remains unevaluated until the complete cascade exists.

## Per-label coverage

| Label | Support | Matched | Correct | Coverage | Correct / support |
|---|---:|---:|---:|---:|---:|
`, report.DatasetVersion, report.CaseCount, report.RubricVersion, report.RecognizerVersion, report.CandidateVersion,
		report.HumanReviewed, report.BaselineEligible, report.Coverage, report.SelectiveAccuracy, report.FalseShortCircuitRate,
		report.SevereShortCircuitRate, report.ConflictCount, report.P95LatencyMicros, report.PatternGatePassed, report.G4Evaluated)
	if err != nil {
		return err
	}
	for _, label := range intent.Labels() {
		metric := report.LabelMetrics[label]
		if _, err := fmt.Fprintf(writer, "| `%s` | %d | %d | %d | %.4f | %.4f |\n", label, metric.Support, metric.Matched, metric.Correct, metric.Coverage, metric.SelectiveRecall); err != nil {
			return err
		}
	}
	return nil
}
