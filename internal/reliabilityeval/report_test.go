package reliabilityeval

import (
	"context"
	"os"
	"path/filepath"
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

func TestFileStorePersistsAndRejectsTamperedReliabilityReport(t *testing.T) {
	report, err := Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "reliability.json")
	store := NewFileStore(path)
	if err := store.Save(report); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.ReportSHA256 != report.ReportSHA256 {
		t.Fatalf("unexpected stored report: report=%+v err=%v", loaded, err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for index := range encoded {
		if encoded[index] == '2' {
			encoded[index] = '3'
			break
		}
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("tampered reliability report was accepted")
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
