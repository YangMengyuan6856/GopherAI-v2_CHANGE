package memory

import (
	"errors"
	"fmt"
	"strings"

	"GopherAI/internal/harness"
)

var ErrCheckpointUnavailable = errors.New("diagnostic checkpoint is unavailable")

func BuildHarnessContextReport(detail harness.RunDetail, working []WorkingMessage, budget int) (CompressionReport, error) {
	if detail.Checkpoint == nil {
		return CompressionReport{}, ErrCheckpointUnavailable
	}
	summary := summaryFromCheckpoint(*detail.Checkpoint, detail.Steps)
	question := latestUserQuestion(working)
	assembly := NewAssembler().Assemble(AssembleInput{
		SafetyRules: []string{
			"只使用当前用户有权访问的会话、检查点与证据。",
			"当前用户明确输入优先于旧摘要；冲突时不得静默沿用旧值。",
			"大日志和工具原文只通过受控引用访问，不把未截断原文复制进 Prompt。",
		},
		CurrentQuestion: question,
		CurrentRunState: fmt.Sprintf("%s v%d", detail.Run.State, detail.Run.StateVersion),
		Summary:         summary, WorkingMessages: working, BudgetTokens: budget,
	})
	report := CompressionReport{
		SchemaVersion: CompressionSchema, RunID: detail.Run.RunID, RunState: string(detail.Run.State), StateVersion: detail.Run.StateVersion,
		HarnessVersion: detail.Run.HarnessVersion, AssemblerVersion: AssemblerVersion, StructuredSummary: summary,
		Context: assembly, SourceTokens: assembly.OriginalTokens, AssembledTokens: assembly.EstimatedTokens,
		TokenReductionRatio: assembly.TokenReductionRatio,
		Limitations: []string{
			"Token 数为稳定的本地估算值，不冒充模型供应商账单 Token。",
			"结构化摘要来自可恢复 Checkpoint 和公开 Step，不包含隐藏思维链。",
			"大日志与工具原文只保留受控引用；需要时由后续工具按权限读取。",
		},
	}
	report.Retention = measureRetention(summary, assembly.Included)
	return report, nil
}

func summaryFromCheckpoint(checkpoint harness.CheckpointState, steps []harness.PublicStep) StructuredSummary {
	summary := StructuredSummary{
		Goal: checkpoint.Goal, Constraints: append([]string(nil), checkpoint.Constraints...),
		ConfirmedFacts: copyFacts(checkpoint.ConfirmedFacts), OpenQuestions: append([]string(nil), checkpoint.OpenQuestions...),
		CompletedSteps: append([]string(nil), checkpoint.CompletedSteps...), FailedSteps: append([]string(nil), checkpoint.FailedSteps...),
		EvidenceRefs: append([]string(nil), checkpoint.EvidenceRefs...), NextAction: checkpoint.NextAction,
	}
	for _, step := range steps {
		label := strings.TrimSpace(step.PublicSummary)
		if label == "" {
			label = strings.TrimSpace(step.StepID)
		}
		if label == "" {
			continue
		}
		if step.Status == "stopped" || step.ErrorCode != "" {
			summary.FailedSteps = appendUnique(summary.FailedSteps, label)
		} else if step.Status == "completed" || step.FinishedAt != nil {
			summary.CompletedSteps = appendUnique(summary.CompletedSteps, label)
		}
		for _, ref := range step.EvidenceRefs {
			summary.EvidenceRefs = appendUnique(summary.EvidenceRefs, ref)
		}
	}
	return summary
}

func measureRetention(summary StructuredSummary, included []ContextItem) ContextRetention {
	counts := make(map[ContextKind]int)
	for _, item := range included {
		counts[item.Kind]++
	}
	return ContextRetention{
		Goal:           metric(counts[ContextGoal], boolCount(strings.TrimSpace(summary.Goal) != "")),
		Constraints:    metric(counts[ContextConstraint], len(boundedStrings(summary.Constraints, 16))),
		ConfirmedFacts: metric(counts[ContextFact], boundedFactCount(summary.ConfirmedFacts)),
		OpenQuestions:  metric(counts[ContextOpenQuestion], len(boundedStrings(summary.OpenQuestions, 16))),
		CompletedSteps: metric(counts[ContextCompleted], len(boundedStrings(summary.CompletedSteps, 32))),
		FailedSteps:    metric(counts[ContextFailed], len(boundedStrings(summary.FailedSteps, 16))),
		EvidenceRefs:   metric(counts[ContextEvidence], len(boundedStrings(summary.EvidenceRefs, 32))),
		NextAction:     metric(counts[ContextNextAction], boolCount(strings.TrimSpace(summary.NextAction) != "")),
	}
}

func metric(retained, expected int) RetentionMetric {
	rate := 1.0
	if expected > 0 {
		rate = float64(retained) / float64(expected)
	}
	return RetentionMetric{Retained: retained, Expected: expected, Rate: rate}
}

func boundedFactCount(facts map[string]string) int {
	count := 0
	for key, value := range facts {
		if boundedText(key, 120) != "" && boundedText(value, 2000) != "" {
			count++
		}
	}
	return count
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func latestUserQuestion(messages []WorkingMessage) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == RoleUser {
			return messages[index].Content
		}
	}
	return ""
}

func copyFacts(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func appendUnique(values []string, candidate string) []string {
	candidate = strings.TrimSpace(candidate)
	for _, value := range values {
		if strings.TrimSpace(value) == candidate {
			return values
		}
	}
	return append(values, candidate)
}
