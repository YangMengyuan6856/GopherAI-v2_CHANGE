package controlrecommendation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	evaldomain "GopherAI/internal/evaluation"
)

const DefaultUnifiedReportPath = "evals/results/devsupport-eval-run-v1-candidate.json"
const maximumEvaluationReportBytes = 4 << 20

type FileEvaluationReader struct{ path string }

func NewFileEvaluationReader(path string) *FileEvaluationReader {
	return &FileEvaluationReader{path: strings.TrimSpace(path)}
}

func (reader *FileEvaluationReader) Load() (EvaluationGate, error) {
	if reader == nil || reader.path == "" {
		return EvaluationGate{}, errors.New("controller evaluation report path is required")
	}
	encoded, err := os.ReadFile(reader.path)
	if err != nil || len(encoded) == 0 || len(encoded) > maximumEvaluationReportBytes {
		return EvaluationGate{}, errors.New("controller evaluation report is unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var report evaldomain.UnifiedEvaluationReport
	if err := decoder.Decode(&report); err != nil {
		return EvaluationGate{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return EvaluationGate{}, errors.New("controller evaluation report has trailing content")
	}
	if err := evaldomain.ValidateUnifiedEvaluationReport(report); err != nil {
		return EvaluationGate{}, err
	}
	digest := sha256.Sum256(encoded)
	return EvaluationGate{
		Source: "unified_evaluation_report", RunID: report.RunID, CandidateVersion: report.CandidateVersion,
		ReportSHA256: hex.EncodeToString(digest[:]), TechnicalGatesPassed: report.Decision.TechnicalGatesPassed,
		HumanReviewed: report.Decision.HumanReviewed, BaselineEligible: report.Decision.BaselineEligible,
	}, nil
}
