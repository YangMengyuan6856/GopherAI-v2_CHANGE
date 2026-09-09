package memory

import (
	"strings"
	"testing"
	"time"

	"GopherAI/internal/harness"
)

func TestBuildHarnessContextReportRetainsStructuredCheckpointBeforeOldMessages(t *testing.T) {
	now := time.Date(2026, 9, 5, 4, 30, 0, 0, time.UTC)
	finished := now
	detail := harness.RunDetail{
		Run: harness.Run{RunID: "run-1", State: harness.StateWaitingUser, StateVersion: 5, HarnessVersion: harness.Version},
		Steps: []harness.PublicStep{
			{StepID: "context", Attempt: 1, Kind: "context", Status: "completed", PublicSummary: "已完成日志脱敏", StateVersion: 2, FinishedAt: &finished},
			{StepID: "tool", Attempt: 1, Kind: "tool", Status: "stopped", PublicSummary: "健康检查超时", ErrorCode: "TOOL_TIMEOUT", StateVersion: 4, FinishedAt: &finished},
		},
		Checkpoint: &harness.CheckpointState{
			Goal: "定位 Redis NOAUTH", Constraints: []string{"只读验证"},
			ConfirmedFacts: map[string]string{"redis_version": "7.4"}, OpenQuestions: []string{"Redis 地址是什么？"},
			EvidenceRefs: []string{"OBS-1"}, NextAction: "等待用户补充地址",
		},
	}
	working := []WorkingMessage{
		{Role: RoleUser, Content: strings.Repeat("很早的重复日志 ", 300)},
		{Role: RoleAssistant, Content: strings.Repeat("很早的排查说明 ", 300)},
		{Role: RoleUser, Content: "当前补充问题"},
	}
	report, err := BuildHarnessContextReport(detail, working, 512)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != CompressionSchema || report.RunID != "run-1" || report.Context.OverBudget {
		t.Fatalf("unexpected report metadata: %+v", report)
	}
	if report.SourceTokens <= report.AssembledTokens || report.TokenReductionRatio <= 0 {
		t.Fatalf("old messages were not compressed: %+v", report)
	}
	for name, item := range map[string]RetentionMetric{
		"goal": report.Retention.Goal, "constraints": report.Retention.Constraints,
		"facts": report.Retention.ConfirmedFacts, "open": report.Retention.OpenQuestions,
		"completed": report.Retention.CompletedSteps, "failed": report.Retention.FailedSteps,
		"evidence": report.Retention.EvidenceRefs, "next": report.Retention.NextAction,
	} {
		if item.Expected == 0 || item.Rate != 1 {
			t.Fatalf("%s was not fully retained: %+v", name, item)
		}
	}
}

func TestBuildHarnessContextReportRequiresCheckpoint(t *testing.T) {
	_, err := BuildHarnessContextReport(harness.RunDetail{Run: harness.Run{RunID: "run-1"}}, nil, 512)
	if err != ErrCheckpointUnavailable {
		t.Fatalf("unexpected error: %v", err)
	}
}
