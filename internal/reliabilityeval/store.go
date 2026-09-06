package reliabilityeval

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const maximumStoredReportBytes = 1 << 20

type ReportStore interface {
	Load() (Report, error)
	Save(Report) error
}

type FileStore struct{ path string }

func NewFileStore(path string) *FileStore { return &FileStore{path: path} }

func (store *FileStore) Load() (Report, error) {
	if store == nil || filepath.Clean(store.path) == "." {
		return Report{}, errors.New("reliability report path is required")
	}
	file, err := os.Open(store.path)
	if err != nil {
		return Report{}, err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maximumStoredReportBytes+1))
	if err != nil || len(encoded) == 0 || len(encoded) > maximumStoredReportBytes {
		return Report{}, errors.New("stored reliability report is unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var report Report
	if err := decoder.Decode(&report); err != nil {
		return Report{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Report{}, errors.New("stored reliability report has trailing content")
	}
	if err := ValidateReport(report); err != nil {
		return Report{}, err
	}
	return report, nil
}

func (store *FileStore) Save(report Report) error {
	if store == nil || filepath.Clean(store.path) == "." {
		return errors.New("reliability report path is required")
	}
	if err := ValidateReport(report); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(store.path), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(store.path), ".reliability-*.tmp")
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
