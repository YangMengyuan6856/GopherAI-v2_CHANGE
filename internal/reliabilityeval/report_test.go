package reliabilityeval

import (
	"context"
	"testing"
	"time"
)

func TestReliabilityAcceptanceCoversRecoveryAndSSECancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || !report.Simulation || report.Mode != Mode || report.GeneratedAt.IsZero() || len(report.ReportSHA256) != 64 {
		t.Fatalf("unexpected reliability report: %+v", report)
	}
	if !report.AgentRecovery.Passed || report.AgentRecovery.RecoveryRate != 1 || !report.AgentRecovery.CheckpointRecovered || report.AgentRecovery.DuplicateResumeExecutions != 0 || report.AgentRecovery.FinalState != "SUCCEEDED" || !report.AgentRecovery.StateVersionsMonotonic {
		t.Fatalf("recovery acceptance failed: %+v", report.AgentRecovery)
	}
	if !report.SSECancellation.Passed || report.SSECancellation.Streams != StreamCount || report.SSECancellation.CancellationObserved != StreamCount || report.SSECancellation.DuplicateFinalEvents != 0 || report.SSECancellation.ActiveWorkersAfter != 0 || !report.SSECancellation.ResourceConverged {
		t.Fatalf("SSE cancellation acceptance failed: %+v", report.SSECancellation)
	}
}

func TestCancellationLoadIsBounded(t *testing.T) {
	if _, err := runCancellation(context.Background(), 0); err == nil {
		t.Fatal("zero stream count was accepted")
	}
	if _, err := runCancellation(context.Background(), 101); err == nil {
		t.Fatal("unbounded stream count was accepted")
	}
}
