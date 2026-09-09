package evolution

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestControlAcceptanceProvesCASAndRollbackWithoutProductionWrites(t *testing.T) {
	report, err := RunControlAcceptance(context.Background(), time.Unix(700, 0))
	if err != nil {
		t.Fatal(err)
	}
	if report.PassedCount != 10 || report.CaseCount != 10 || report.ProductionWrites != 0 || report.ProductionActivePointers != 0 {
		t.Fatalf("unexpected harness control acceptance: %+v", report)
	}
}

func TestControlAcceptanceStoreRejectsTampering(t *testing.T) {
	report, err := RunControlAcceptance(context.Background(), time.Unix(700, 0))
	if err != nil {
		t.Fatal(err)
	}
	store := NewFileControlAcceptanceStore(filepath.Join(t.TempDir(), "control.json"))
	if err := store.Save(report); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.ReportSHA256 != report.ReportSHA256 {
		t.Fatalf("stored report did not round trip: %v", err)
	}
	report.ProductionWrites = 1
	if err := store.Save(report); err == nil {
		t.Fatal("tampered production-write claim was accepted")
	}
}
