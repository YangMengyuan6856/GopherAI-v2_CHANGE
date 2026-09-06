package evaluation

import (
	"path/filepath"
	"testing"
)

func TestJudgeCalibrationReportStoreRoundTripAndTamperDetection(t *testing.T) {
	report := calibrationReport(t)
	path := filepath.Join(t.TempDir(), "judge-calibration.json")
	if err := WriteJudgeCalibrationReport(path, report); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadJudgeCalibrationReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ReportSHA256 != report.ReportSHA256 || loaded.CaseCount != JudgeCalibrationCaseCount {
		t.Fatalf("unexpected report round trip: %+v", loaded)
	}
	loaded.Cases[0].Scores.Safety = 0
	if err := ValidateJudgeCalibrationReport(loaded, true); err == nil {
		t.Fatal("expected tampered score or report hash rejection")
	}
}
