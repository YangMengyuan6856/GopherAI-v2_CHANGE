package evolution

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const maximumControlAcceptanceBytes = 1 << 20

type ControlAcceptanceStore interface {
	Load() (ControlAcceptanceReport, error)
	Save(ControlAcceptanceReport) error
}

type FileControlAcceptanceStore struct{ path string }

func NewFileControlAcceptanceStore(path string) *FileControlAcceptanceStore {
	return &FileControlAcceptanceStore{path: path}
}

func (store *FileControlAcceptanceStore) Load() (ControlAcceptanceReport, error) {
	if store == nil || filepath.Clean(store.path) == "." {
		return ControlAcceptanceReport{}, errors.New("harness control acceptance path is required")
	}
	file, err := os.Open(store.path)
	if err != nil {
		return ControlAcceptanceReport{}, err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maximumControlAcceptanceBytes+1))
	if err != nil || len(encoded) == 0 || len(encoded) > maximumControlAcceptanceBytes {
		return ControlAcceptanceReport{}, errors.New("stored harness control acceptance is unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var report ControlAcceptanceReport
	if err := decoder.Decode(&report); err != nil {
		return ControlAcceptanceReport{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ControlAcceptanceReport{}, errors.New("stored harness control acceptance has trailing content")
	}
	if err := ValidateControlAcceptanceReport(report); err != nil {
		return ControlAcceptanceReport{}, err
	}
	return report, nil
}

func (store *FileControlAcceptanceStore) Save(report ControlAcceptanceReport) error {
	if store == nil || filepath.Clean(store.path) == "." {
		return errors.New("harness control acceptance path is required")
	}
	if err := ValidateControlAcceptanceReport(report); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(store.path), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(store.path), ".harness-control-*.tmp")
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
