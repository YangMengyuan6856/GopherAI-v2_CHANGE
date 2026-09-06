package main

import "testing"

func TestJudgeCalibrationRejectsArbitraryPathsBeforeModelInitialization(t *testing.T) {
	if err := run("other.jsonl", defaultReportPath); err == nil {
		t.Fatal("expected fixed dataset path guard")
	}
	if err := run(defaultDatasetPath, "other.json"); err == nil {
		t.Fatal("expected fixed report path guard")
	}
}
