package evaluation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maxJudgeCalibrationReportBytes = 4 << 20

func WriteJudgeCalibrationReport(path string, report JudgeCalibrationReport) error {
	if path == "" {
		return errors.New("judge calibration report path is required")
	}
	if err := ValidateJudgeCalibrationReport(report, true); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".judge-calibration-*.json")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func LoadJudgeCalibrationReport(path string) (JudgeCalibrationReport, error) {
	file, err := os.Open(path)
	if err != nil {
		return JudgeCalibrationReport{}, err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxJudgeCalibrationReportBytes+1))
	if err != nil || len(encoded) == 0 || len(encoded) > maxJudgeCalibrationReportBytes {
		return JudgeCalibrationReport{}, fmt.Errorf("judge calibration report is unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var report JudgeCalibrationReport
	if err := decoder.Decode(&report); err != nil {
		return JudgeCalibrationReport{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return JudgeCalibrationReport{}, errors.New("judge calibration report has trailing content")
	}
	if err := ValidateJudgeCalibrationReport(report, true); err != nil {
		return JudgeCalibrationReport{}, err
	}
	return report, nil
}
