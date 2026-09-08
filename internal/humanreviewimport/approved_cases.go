package humanreviewimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const ApprovedCaseManifestSchemaVersion = "human-review-approved-case-commitments-v1"

type ApprovedCaseManifest struct {
	SchemaVersion           string                   `json:"schema_version"`
	SourceCatalogSHA256     string                   `json:"source_catalog_sha256"`
	SourceConclusionsSHA256 string                   `json:"source_conclusions_sha256"`
	Cases                   []ApprovedCaseCommitment `json:"cases"`
}

func LoadApprovedCaseManifest(reader io.Reader, expectedConclusionsSHA string) (ApprovedCaseManifest, error) {
	if reader == nil {
		return ApprovedCaseManifest{}, ErrInvalidConclusions
	}
	encoded, err := io.ReadAll(io.LimitReader(reader, 1<<20))
	if err != nil || len(encoded) == 0 || len(encoded) >= 1<<20 {
		return ApprovedCaseManifest{}, ErrInvalidConclusions
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var manifest ApprovedCaseManifest
	if err := decoder.Decode(&manifest); err != nil {
		return ApprovedCaseManifest{}, fmt.Errorf("%w: approved case manifest", ErrInvalidConclusions)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ApprovedCaseManifest{}, fmt.Errorf("%w: approved case manifest trailing content", ErrInvalidConclusions)
	}
	if manifest.SchemaVersion != ApprovedCaseManifestSchemaVersion || !isSHA256(manifest.SourceCatalogSHA256) || manifest.SourceConclusionsSHA256 != strings.ToLower(strings.TrimSpace(expectedConclusionsSHA)) || len(manifest.Cases) == 0 {
		return ApprovedCaseManifest{}, fmt.Errorf("%w: approved case manifest identity", ErrInvalidConclusions)
	}
	seen := make(map[int]struct{}, len(manifest.Cases))
	for _, item := range manifest.Cases {
		if item.Ordinal < 1 || item.Ordinal > FullCaseCount || strings.TrimSpace(item.CaseID) == "" || !isSHA256(item.CaseSHA256) {
			return ApprovedCaseManifest{}, fmt.Errorf("%w: approved case commitment", ErrInvalidConclusions)
		}
		if _, duplicate := seen[item.Ordinal]; duplicate {
			return ApprovedCaseManifest{}, fmt.Errorf("%w: duplicate approved ordinal", ErrInvalidConclusions)
		}
		seen[item.Ordinal] = struct{}{}
	}
	return manifest, nil
}

func (manifest ApprovedCaseManifest) ByOrdinal() map[int]ApprovedCaseCommitment {
	result := make(map[int]ApprovedCaseCommitment, len(manifest.Cases))
	for _, item := range manifest.Cases {
		result[item.Ordinal] = item
	}
	return result
}
