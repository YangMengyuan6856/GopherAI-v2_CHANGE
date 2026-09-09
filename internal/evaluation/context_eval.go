package evaluation

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"GopherAI/internal/harness"
	memorydomain "GopherAI/internal/memory"
)

const ContextEvaluatorVersion = "context-compression-evaluator-v1"

type ContextEvaluationMetrics struct {
	CaseCount               int            `json:"case_count"`
	OutcomeCounts           map[string]int `json:"outcome_counts"`
	ConstraintRetention     float64        `json:"constraint_retention"`
	ConfirmedFactRetention  float64        `json:"confirmed_fact_retention"`
	OpenQuestionRetention   float64        `json:"open_question_retention"`
	NextActionRetention     float64        `json:"next_action_retention"`
	AverageTokenReduction   float64        `json:"average_token_reduction"`
	OverBudgetCases         int            `json:"over_budget_cases"`
	DeterministicReplayRate float64        `json:"deterministic_replay_rate"`
}

type ContextEvaluationCaseResult struct {
	ID                    string  `json:"id"`
	Outcome               string  `json:"outcome"`
	SourceTokens          int     `json:"source_tokens"`
	AssembledTokens       int     `json:"assembled_tokens"`
	TokenReduction        float64 `json:"token_reduction"`
	ConstraintRetention   float64 `json:"constraint_retention"`
	FactRetention         float64 `json:"fact_retention"`
	OpenQuestionRetention float64 `json:"open_question_retention"`
	NextActionRetention   float64 `json:"next_action_retention"`
	OverBudget            bool    `json:"over_budget"`
	Deterministic         bool    `json:"deterministic"`
}

type ContextEvaluationReport struct {
	EvaluatorVersion     string                        `json:"evaluator_version"`
	DatasetVersion       string                        `json:"dataset_version"`
	GeneratedAt          time.Time                     `json:"generated_at"`
	HumanReviewed        bool                          `json:"human_reviewed"`
	BaselineEligible     bool                          `json:"baseline_eligible"`
	TechnicalGatesPassed bool                          `json:"technical_gates_passed"`
	GateFailures         []string                      `json:"gate_failures,omitempty"`
	Metrics              ContextEvaluationMetrics      `json:"metrics"`
	Cases                []ContextEvaluationCaseResult `json:"cases"`
}

func EvaluateContextCompression(cases []ContextCase, summary ContextDatasetSummary, generatedAt time.Time) ContextEvaluationReport {
	report := ContextEvaluationReport{
		EvaluatorVersion: ContextEvaluatorVersion, DatasetVersion: ContextDatasetVersion, GeneratedAt: generatedAt.UTC(),
		HumanReviewed: summary.HumanReviewed, Cases: make([]ContextEvaluationCaseResult, 0, len(cases)),
		Metrics: ContextEvaluationMetrics{CaseCount: len(cases), OutcomeCounts: summary.OutcomeCounts},
	}
	var constraintRetained, constraintExpected, factRetained, factExpected int
	var openRetained, openExpected, nextRetained, nextExpected, deterministic int
	for _, item := range cases {
		detail, working := contextFixture(item, generatedAt.UTC())
		first, firstErr := memorydomain.BuildHarnessContextReport(detail, working, item.BudgetTokens)
		second, secondErr := memorydomain.BuildHarnessContextReport(detail, working, item.BudgetTokens)
		stable := firstErr == nil && secondErr == nil && reflect.DeepEqual(first.Context, second.Context) && reflect.DeepEqual(first.Retention, second.Retention)
		result := ContextEvaluationCaseResult{ID: item.ID, Outcome: item.Outcome, Deterministic: stable}
		if firstErr == nil {
			result.SourceTokens, result.AssembledTokens, result.TokenReduction = first.SourceTokens, first.AssembledTokens, first.TokenReductionRatio
			result.ConstraintRetention, result.FactRetention = first.Retention.Constraints.Rate, first.Retention.ConfirmedFacts.Rate
			result.OpenQuestionRetention, result.NextActionRetention = first.Retention.OpenQuestions.Rate, first.Retention.NextAction.Rate
			result.OverBudget = first.Context.OverBudget
			constraintRetained += first.Retention.Constraints.Retained
			constraintExpected += first.Retention.Constraints.Expected
			factRetained += first.Retention.ConfirmedFacts.Retained
			factExpected += first.Retention.ConfirmedFacts.Expected
			openRetained += first.Retention.OpenQuestions.Retained
			openExpected += first.Retention.OpenQuestions.Expected
			nextRetained += first.Retention.NextAction.Retained
			nextExpected += first.Retention.NextAction.Expected
			report.Metrics.AverageTokenReduction += first.TokenReductionRatio
			if first.Context.OverBudget {
				report.Metrics.OverBudgetCases++
			}
		}
		if stable {
			deterministic++
		}
		report.Cases = append(report.Cases, result)
	}
	count := len(cases)
	report.Metrics.ConstraintRetention = safeRate(constraintRetained, constraintExpected)
	report.Metrics.ConfirmedFactRetention = safeRate(factRetained, factExpected)
	report.Metrics.OpenQuestionRetention = safeRate(openRetained, openExpected)
	report.Metrics.NextActionRetention = safeRate(nextRetained, nextExpected)
	if count > 0 {
		report.Metrics.AverageTokenReduction /= float64(count)
		report.Metrics.DeterministicReplayRate = float64(deterministic) / float64(count)
	}
	if report.Metrics.ConstraintRetention < 0.95 {
		report.GateFailures = append(report.GateFailures, "constraint_retention_below_0.95")
	}
	if report.Metrics.ConfirmedFactRetention < 0.95 {
		report.GateFailures = append(report.GateFailures, "confirmed_fact_retention_below_0.95")
	}
	if report.Metrics.OpenQuestionRetention < 0.90 {
		report.GateFailures = append(report.GateFailures, "open_question_retention_below_0.90")
	}
	if report.Metrics.NextActionRetention < 0.95 {
		report.GateFailures = append(report.GateFailures, "next_action_retention_below_0.95")
	}
	if report.Metrics.OverBudgetCases != 0 {
		report.GateFailures = append(report.GateFailures, "context_budget_exceeded")
	}
	if report.Metrics.DeterministicReplayRate != 1 {
		report.GateFailures = append(report.GateFailures, "deterministic_replay_below_1.0")
	}
	report.TechnicalGatesPassed = len(report.GateFailures) == 0
	report.BaselineEligible = report.TechnicalGatesPassed && report.HumanReviewed
	return report
}

func MarshalContextReport(report ContextEvaluationReport) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

func contextFixture(item ContextCase, now time.Time) (harness.RunDetail, []memorydomain.WorkingMessage) {
	state := harness.StateSucceeded
	if item.Outcome == "clarify" {
		state = harness.StateWaitingUser
	} else if item.Outcome == "resume" {
		state = harness.StateContextReady
	} else if item.Outcome == "refuse" {
		state = harness.StateCancelled
	}
	checkpoint := harness.CheckpointState{
		Goal: item.Summary.Goal, Constraints: item.Summary.Constraints, ConfirmedFacts: item.Summary.ConfirmedFacts,
		OpenQuestions: item.Summary.OpenQuestions, CompletedSteps: item.Summary.CompletedSteps, FailedSteps: item.Summary.FailedSteps,
		EvidenceRefs: item.Summary.EvidenceRefs, NextAction: item.Summary.NextAction,
	}
	detail := harness.RunDetail{Run: harness.Run{RunID: item.ID, State: state, StateVersion: 5, HarnessVersion: harness.Version}, Checkpoint: &checkpoint}
	working := make([]memorydomain.WorkingMessage, 0, item.NoiseTurns+1)
	for turn := 0; turn < item.NoiseTurns; turn++ {
		role := memorydomain.RoleUser
		if turn%2 == 1 {
			role = memorydomain.RoleAssistant
		}
		working = append(working, memorydomain.WorkingMessage{ID: uint(turn + 1), Role: role, Content: fmt.Sprintf("历史轮次 %02d：%s", turn+1, strings.Repeat(item.NoiseText+" ", 6)), CreatedAt: now.Add(time.Duration(turn) * time.Minute)})
	}
	working = append(working, memorydomain.WorkingMessage{ID: uint(item.NoiseTurns + 1), Role: memorydomain.RoleUser, Content: item.CurrentQuestion, CreatedAt: now.Add(time.Duration(item.NoiseTurns+1) * time.Minute)})
	return detail, working
}

func safeRate(retained, expected int) float64 {
	if expected == 0 {
		return 1
	}
	return float64(retained) / float64(expected)
}
