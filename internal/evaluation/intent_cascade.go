package evaluation

import (
	"GopherAI/internal/intent"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

type CascadeIntentRecognizer interface {
	Recognize(ctx context.Context, input intent.CascadeInput) intent.CascadeDecision
}

type IntentLabelMetrics struct {
	Support   int     `json:"support"`
	Predicted int     `json:"predicted"`
	Correct   int     `json:"correct"`
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	F1        float64 `json:"f1"`
}

type IntentStageMetrics struct {
	Count    int     `json:"count"`
	Correct  int     `json:"correct"`
	Accuracy float64 `json:"accuracy"`
}

type IntentConfidenceBin struct {
	Lower             float64 `json:"lower"`
	Upper             float64 `json:"upper"`
	Count             int     `json:"count"`
	Correct           int     `json:"correct"`
	AverageConfidence float64 `json:"average_confidence"`
	Accuracy          float64 `json:"accuracy"`
}

type IntentCascadeFailure struct {
	ID             string   `json:"id"`
	Expected       string   `json:"expected"`
	Predicted      string   `json:"predicted"`
	Confidence     float64  `json:"confidence"`
	FinalStage     string   `json:"final_stage"`
	SevereMisroute bool     `json:"severe_misroute"`
	ReasonCodes    []string `json:"reason_codes,omitempty"`
}

type IntentCascadeReport struct {
	SchemaVersion       string                        `json:"schema_version"`
	DatasetVersion      string                        `json:"dataset_version"`
	RubricVersion       string                        `json:"rubric_version"`
	RecognizerVersion   string                        `json:"recognizer_version"`
	CandidateVersion    string                        `json:"candidate_version"`
	GeneratedAt         time.Time                     `json:"generated_at"`
	CaseCount           int                           `json:"case_count"`
	HumanReviewed       bool                          `json:"human_reviewed"`
	BaselineEligible    bool                          `json:"baseline_eligible"`
	Accuracy            float64                       `json:"accuracy"`
	MacroF1             float64                       `json:"macro_f1"`
	MinimumRecall       float64                       `json:"minimum_recall"`
	SevereMisrouteRate  float64                       `json:"severe_misroute_rate"`
	PrototypeCallRate   float64                       `json:"prototype_call_rate"`
	LLMCallRate         float64                       `json:"llm_call_rate"`
	ExpectedCalibration float64                       `json:"expected_calibration_error"`
	P95LatencyMillis    int64                         `json:"p95_latency_ms"`
	TechnicalGatePassed bool                          `json:"technical_gate_passed"`
	G4Evaluated         bool                          `json:"g4_evaluated"`
	LabelMetrics        map[string]IntentLabelMetrics `json:"label_metrics"`
	StageMetrics        map[string]IntentStageMetrics `json:"stage_metrics"`
	ConfidenceBins      []IntentConfidenceBin         `json:"confidence_bins"`
	Confusion           map[string]map[string]int     `json:"confusion"`
	Failures            []IntentCascadeFailure        `json:"failures"`
}

type confidenceAccumulator struct {
	IntentConfidenceBin
	confidenceSum float64
}

func EvaluateCascade(ctx context.Context, cases []IntentCase, recognizer CascadeIntentRecognizer, candidateVersion string, now time.Time) IntentCascadeReport {
	report := IntentCascadeReport{
		SchemaVersion: "1", DatasetVersion: IntentDatasetVersion, RubricVersion: intent.RubricVersion,
		RecognizerVersion: intent.CascadeVersion, CandidateVersion: strings.TrimSpace(candidateVersion), GeneratedAt: now.UTC(),
		CaseCount: len(cases), HumanReviewed: true, G4Evaluated: true,
		LabelMetrics: make(map[string]IntentLabelMetrics, 6), StageMetrics: make(map[string]IntentStageMetrics, 5),
		Confusion: make(map[string]map[string]int, 6), Failures: make([]IntentCascadeFailure, 0),
	}
	bins := []confidenceAccumulator{
		{IntentConfidenceBin: IntentConfidenceBin{Lower: 0, Upper: .6}},
		{IntentConfidenceBin: IntentConfidenceBin{Lower: .6, Upper: .8}},
		{IntentConfidenceBin: IntentConfidenceBin{Lower: .8, Upper: .9}},
		{IntentConfidenceBin: IntentConfidenceBin{Lower: .9, Upper: 1.000001}},
	}
	latencies := make([]int64, 0, len(cases))
	correctCount, severeCount, prototypeCalls, llmCalls := 0, 0, 0, 0
	for _, item := range cases {
		if item.ReviewedBy != "human" {
			report.HumanReviewed = false
		}
		started := time.Now()
		decision := recognizer.Recognize(ctx, intent.CascadeInput{Question: item.Question, PreviousIntent: item.Context.PreviousIntent})
		elapsedMillis := time.Since(started).Milliseconds()
		if decision.Diagnostics.LatencyMillis > elapsedMillis {
			elapsedMillis = decision.Diagnostics.LatencyMillis
		}
		latencies = append(latencies, elapsedMillis)
		predicted := decision.Result.Intent
		if !intent.IsKnown(predicted) {
			predicted = "unknown"
		}
		confidence := clampUnit(decision.Result.Confidence)
		correct := predicted == item.Expected.Intent
		if correct {
			correctCount++
		}
		severe := intent.IsSevereMisroute(item.Expected.Intent, predicted)
		if severe {
			severeCount++
		}
		if decision.Diagnostics.PrototypeCalled {
			prototypeCalls++
		}
		if decision.Diagnostics.LLMCalled {
			llmCalls++
		}

		expectedMetric := report.LabelMetrics[item.Expected.Intent]
		expectedMetric.Support++
		if correct {
			expectedMetric.Correct++
		}
		report.LabelMetrics[item.Expected.Intent] = expectedMetric
		predictedMetric := report.LabelMetrics[predicted]
		predictedMetric.Predicted++
		report.LabelMetrics[predicted] = predictedMetric
		if report.Confusion[item.Expected.Intent] == nil {
			report.Confusion[item.Expected.Intent] = make(map[string]int, 7)
		}
		report.Confusion[item.Expected.Intent][predicted]++
		stage := boundedEvalStage(decision.Diagnostics.FinalStage)
		stageMetric := report.StageMetrics[stage]
		stageMetric.Count++
		if correct {
			stageMetric.Correct++
		}
		report.StageMetrics[stage] = stageMetric
		for index := range bins {
			if confidence >= bins[index].Lower && confidence < bins[index].Upper {
				bins[index].Count++
				bins[index].confidenceSum += confidence
				if correct {
					bins[index].Correct++
				}
				break
			}
		}
		if !correct {
			report.Failures = append(report.Failures, IntentCascadeFailure{
				ID: item.ID, Expected: item.Expected.Intent, Predicted: predicted, Confidence: confidence,
				FinalStage: stage, SevereMisroute: severe, ReasonCodes: cascadeReasonCodes(decision),
			})
		}
	}

	report.MinimumRecall = 1
	for _, label := range intent.Labels() {
		metric := report.LabelMetrics[label]
		if metric.Predicted > 0 {
			metric.Precision = float64(metric.Correct) / float64(metric.Predicted)
		}
		if metric.Support > 0 {
			metric.Recall = float64(metric.Correct) / float64(metric.Support)
		}
		if metric.Precision+metric.Recall > 0 {
			metric.F1 = 2 * metric.Precision * metric.Recall / (metric.Precision + metric.Recall)
		}
		report.MacroF1 += metric.F1
		if metric.Recall < report.MinimumRecall {
			report.MinimumRecall = metric.Recall
		}
		report.LabelMetrics[label] = metric
	}
	if len(intent.Labels()) > 0 {
		report.MacroF1 /= float64(len(intent.Labels()))
	}
	for stage, metric := range report.StageMetrics {
		if metric.Count > 0 {
			metric.Accuracy = float64(metric.Correct) / float64(metric.Count)
		}
		report.StageMetrics[stage] = metric
	}
	for index := range bins {
		if bins[index].Count > 0 {
			bins[index].AverageConfidence = bins[index].confidenceSum / float64(bins[index].Count)
			bins[index].Accuracy = float64(bins[index].Correct) / float64(bins[index].Count)
			report.ExpectedCalibration += float64(bins[index].Count) / float64(max(1, len(cases))) * abs(bins[index].AverageConfidence-bins[index].Accuracy)
		}
		bins[index].Upper = min(bins[index].Upper, 1)
		report.ConfidenceBins = append(report.ConfidenceBins, bins[index].IntentConfidenceBin)
	}
	if len(cases) > 0 {
		report.Accuracy = float64(correctCount) / float64(len(cases))
		report.SevereMisrouteRate = float64(severeCount) / float64(len(cases))
		report.PrototypeCallRate = float64(prototypeCalls) / float64(len(cases))
		report.LLMCallRate = float64(llmCalls) / float64(len(cases))
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	if len(latencies) > 0 {
		report.P95LatencyMillis = latencies[(95*len(latencies)+99)/100-1]
	}
	report.TechnicalGatePassed = report.Accuracy >= .92 && report.MacroF1 >= .90 && report.MinimumRecall >= .85 && report.SevereMisrouteRate <= .03
	report.BaselineEligible = report.HumanReviewed && report.TechnicalGatePassed
	return report
}

func WriteIntentCascadeJSON(writer io.Writer, report IntentCascadeReport) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func WriteIntentCascadeMarkdown(writer io.Writer, report IntentCascadeReport) error {
	if writer == nil {
		return fmt.Errorf("report writer is required")
	}
	_, err := fmt.Fprintf(writer, `# Intent Cascade Evaluation

- Dataset: %s (%d cases)
- Rubric: %s
- Recognizer: %s
- Candidate: %s
- Human reviewed: %t
- Baseline eligible: %t

| Metric | Result | Gate |
|---|---:|---:|
| Accuracy | %.4f | >= 0.92 |
| Macro-F1 | %.4f | >= 0.90 |
| Minimum class recall | %.4f | >= 0.85 |
| Severe misroute rate | %.4f | <= 0.03 |
| Prototype call rate | %.4f | observed |
| LLM call rate | %.4f | observed |
| Expected calibration error | %.4f | observed |
| P95 cascade latency | %d ms | observed |
| Technical gate passed | %t | all required gates |

The dataset is not a resume-grade baseline until every case is human reviewed.
A technical pass on pending labels remains a candidate result only.

## Per-label metrics

| Label | Support | Predicted | Precision | Recall | F1 |
|---|---:|---:|---:|---:|---:|
`, report.DatasetVersion, report.CaseCount, report.RubricVersion, report.RecognizerVersion, report.CandidateVersion,
		report.HumanReviewed, report.BaselineEligible, report.Accuracy, report.MacroF1, report.MinimumRecall,
		report.SevereMisrouteRate, report.PrototypeCallRate, report.LLMCallRate, report.ExpectedCalibration,
		report.P95LatencyMillis, report.TechnicalGatePassed)
	if err != nil {
		return err
	}
	for _, label := range intent.Labels() {
		metric := report.LabelMetrics[label]
		if _, err := fmt.Fprintf(writer, "| `%s` | %d | %d | %.4f | %.4f | %.4f |\n", label, metric.Support, metric.Predicted, metric.Precision, metric.Recall, metric.F1); err != nil {
			return err
		}
	}
	return nil
}

func cascadeReasonCodes(decision intent.CascadeDecision) []string {
	reasons := make([]string, 0, 8)
	seen := make(map[string]struct{})
	appendReason := func(reason string) {
		if reason == "" || len(reason) > 64 || len(reasons) >= 8 {
			return
		}
		if _, exists := seen[reason]; exists {
			return
		}
		seen[reason] = struct{}{}
		reasons = append(reasons, reason)
	}
	for _, stage := range decision.Result.Stages {
		appendReason(stage.ReasonCode)
	}
	for _, reason := range decision.Diagnostics.FallbackReasons {
		appendReason(reason)
	}
	return reasons
}

func boundedEvalStage(stage string) string {
	switch stage {
	case "pattern", "prototype", "llm", "degraded_clarification", "unavailable":
		return stage
	default:
		return "unknown"
	}
}

func clampUnit(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
