package evolution

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const maximumComparisonReportBytes = 2 << 20

type ComparisonStore interface {
	Load() (ComparisonReport, error)
	Save(ComparisonReport) error
}

type FileComparisonStore struct{ path string }

func NewFileComparisonStore(path string) *FileComparisonStore {
	return &FileComparisonStore{path: path}
}

func (store *FileComparisonStore) Load() (ComparisonReport, error) {
	if store == nil || filepath.Clean(store.path) == "." {
		return ComparisonReport{}, errors.New("harness evolution comparison path is required")
	}
	file, err := os.Open(store.path)
	if err != nil {
		return ComparisonReport{}, err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maximumComparisonReportBytes+1))
	if err != nil || len(encoded) == 0 || len(encoded) > maximumComparisonReportBytes {
		return ComparisonReport{}, errors.New("stored harness evolution comparison is unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var report ComparisonReport
	if err := decoder.Decode(&report); err != nil {
		return ComparisonReport{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ComparisonReport{}, errors.New("stored harness evolution comparison has trailing content")
	}
	if err := ValidateComparisonReport(report); err != nil {
		return ComparisonReport{}, err
	}
	return report, nil
}

func (store *FileComparisonStore) Save(report ComparisonReport) error {
	if store == nil || filepath.Clean(store.path) == "." {
		return errors.New("harness evolution comparison path is required")
	}
	if err := ValidateComparisonReport(report); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(store.path), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(store.path), ".harness-evolution-comparison-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o640); err != nil {
		temporary.Close()
		return err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, store.path)
}
