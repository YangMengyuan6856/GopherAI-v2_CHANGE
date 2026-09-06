package evaluation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const ReviewManifestSchemaVersion = "evaluation-review-manifest-v1"

type ReviewManifest struct {
	SchemaVersion         string                `json:"schema_version"`
	ManifestVersion       string                `json:"manifest_version"`
	DatasetVersion        string                `json:"dataset_version"`
	CatalogManifestSHA256 string                `json:"catalog_manifest_sha256"`
	CreatedAt             time.Time             `json:"created_at"`
	Slices                []ReviewManifestSlice `json:"slices"`
	Fixtures              []ReviewFixture       `json:"fixtures"`
}

type ReviewManifestSlice struct {
	Name           string     `json:"name"`
	Path           string     `json:"path"`
	DatasetVersion string     `json:"dataset_version"`
	ArtifactSHA256 string     `json:"artifact_sha256"`
	CaseCount      int        `json:"case_count"`
	SourceCategory string     `json:"source_category"`
	ReviewStatus   string     `json:"review_status"`
	Reviewer       string     `json:"reviewer"`
	ReviewedAt     *time.Time `json:"reviewed_at"`
}

type ReviewFixture struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type ReviewManifestValidationReport struct {
	SchemaVersion         string                `json:"schema_version"`
	ManifestVersion       string                `json:"manifest_version"`
	ManifestSHA256        string                `json:"manifest_sha256"`
	DatasetVersion        string                `json:"dataset_version"`
	CatalogManifestSHA256 string                `json:"catalog_manifest_sha256"`
	CatalogMatched        bool                  `json:"catalog_matched"`
	TotalCases            int                   `json:"total_cases"`
	ReviewedCases         int                   `json:"reviewed_cases"`
	PendingCases          int                   `json:"pending_cases"`
	RejectedCases         int                   `json:"rejected_cases"`
	HumanReviewed         bool                  `json:"human_reviewed"`
	BaselineEligible      bool                  `json:"baseline_eligible"`
	Passed                bool                  `json:"passed"`
	Slices                []ReviewManifestSlice `json:"slices"`
	Fixtures              []ReviewFixture       `json:"fixtures"`
	Errors                []string              `json:"errors,omitempty"`
	Guardrails            []string              `json:"guardrails"`
}

func ValidateReviewManifestFile(reviewPath, catalogPath string) (ReviewManifestValidationReport, error) {
	reviewBytes, err := os.ReadFile(reviewPath)
	if err != nil {
		return ReviewManifestValidationReport{}, err
	}
	manifest, err := decodeReviewManifest(reviewBytes)
	if err != nil {
		return ReviewManifestValidationReport{}, err
	}
	catalogBytes, err := os.ReadFile(catalogPath)
	if err != nil {
		return ReviewManifestValidationReport{}, err
	}
	catalogManifest, _, err := LoadEvalCatalogManifest(bytes.NewReader(catalogBytes))
	if err != nil {
		return ReviewManifestValidationReport{}, err
	}
	catalogReport := ValidateEvalCatalog(catalogManifest, catalogBytes, filepath.Dir(catalogPath))
	return validateReviewManifest(manifest, reviewBytes, filepath.Dir(reviewPath), catalogManifest, catalogReport), nil
}

func decodeReviewManifest(encoded []byte) (ReviewManifest, error) {
	if len(encoded) == 0 || len(encoded) > 1<<20 {
		return ReviewManifest{}, errors.New("review manifest is empty or too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var manifest ReviewManifest
	if err := decoder.Decode(&manifest); err != nil {
		return ReviewManifest{}, fmt.Errorf("decode review manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ReviewManifest{}, errors.New("review manifest has trailing content")
	}
	return manifest, nil
}

func validateReviewManifest(manifest ReviewManifest, encoded []byte, baseDirectory string, catalog EvalCatalogManifest, catalogReport EvalCatalogValidationReport) ReviewManifestValidationReport {
	digest := sha256.Sum256(encoded)
	report := ReviewManifestValidationReport{
		SchemaVersion: manifest.SchemaVersion, ManifestVersion: manifest.ManifestVersion,
		ManifestSHA256: hex.EncodeToString(digest[:]), DatasetVersion: manifest.DatasetVersion,
		CatalogManifestSHA256: manifest.CatalogManifestSHA256, Slices: append([]ReviewManifestSlice(nil), manifest.Slices...),
		Fixtures:   append([]ReviewFixture(nil), manifest.Fixtures...),
		Guardrails: []string{"catalog_hash_bound", "slice_hash_bound", "fixture_hash_bound", "reviewer_and_time_required", "per_case_review_required", "pending_user_blocks_baseline"},
	}
	if manifest.SchemaVersion != ReviewManifestSchemaVersion || strings.TrimSpace(manifest.ManifestVersion) == "" || manifest.DatasetVersion != catalog.DatasetVersion || manifest.CreatedAt.IsZero() {
		report.Errors = append(report.Errors, "review manifest identity is invalid")
	}
	report.CatalogMatched = strings.EqualFold(manifest.CatalogManifestSHA256, catalogReport.ManifestSHA256)
	if !report.CatalogMatched || !catalogReport.Passed {
		report.Errors = append(report.Errors, "review manifest is not bound to the validated catalog")
	}
	catalogSlices := make(map[string]EvalCatalogSliceResult, len(catalogReport.Slices))
	catalogDefinitions := make(map[string]EvalCatalogSliceManifest, len(catalog.Slices))
	for index, item := range catalog.Slices {
		catalogDefinitions[item.Name] = item
		if index < len(catalogReport.Slices) {
			catalogSlices[item.Name] = catalogReport.Slices[index]
		}
	}
	seenSlices := map[string]struct{}{}
	for _, slice := range manifest.Slices {
		definition, defined := catalogDefinitions[slice.Name]
		validated := catalogSlices[slice.Name]
		if !defined || slice.Path != definition.Path || slice.CaseCount != definition.ExpectedCount || !strings.EqualFold(slice.ArtifactSHA256, validated.ActualSHA) || !contains(definition.DatasetVersions, slice.DatasetVersion) || strings.TrimSpace(slice.SourceCategory) == "" {
			report.Errors = append(report.Errors, "review slice does not match catalog: "+slice.Name)
			continue
		}
		if _, duplicate := seenSlices[slice.Name]; duplicate {
			report.Errors = append(report.Errors, "duplicate review slice: "+slice.Name)
			continue
		}
		seenSlices[slice.Name] = struct{}{}
		switch slice.ReviewStatus {
		case "pending_user":
			report.PendingCases += slice.CaseCount
			if strings.TrimSpace(slice.Reviewer) != "" || slice.ReviewedAt != nil || validated.ReviewCounts["pending_user"] != slice.CaseCount {
				report.Errors = append(report.Errors, "pending review slice contains forged review evidence: "+slice.Name)
			}
		case "reviewed":
			report.ReviewedCases += slice.CaseCount
			if strings.TrimSpace(slice.Reviewer) == "" || slice.ReviewedAt == nil || validated.ReviewCounts["human"] != slice.CaseCount {
				report.Errors = append(report.Errors, "reviewed slice lacks reviewer, timestamp or per-case human labels: "+slice.Name)
			}
		case "rejected":
			report.RejectedCases += slice.CaseCount
			if strings.TrimSpace(slice.Reviewer) == "" || slice.ReviewedAt == nil {
				report.Errors = append(report.Errors, "rejected slice lacks reviewer or timestamp: "+slice.Name)
			}
		default:
			report.Errors = append(report.Errors, "invalid review status: "+slice.Name)
		}
		report.TotalCases += slice.CaseCount
	}
	if len(seenSlices) != len(catalog.Slices) || report.TotalCases != catalog.TotalCases {
		report.Errors = append(report.Errors, "review manifest does not cover the full catalog")
	}
	seenFixtures := map[string]struct{}{}
	for _, fixture := range manifest.Fixtures {
		if fixture.Name == "" || len(fixture.SHA256) != 64 {
			report.Errors = append(report.Errors, "fixture identity is invalid")
			continue
		}
		path, pathErr := safeCatalogPath(baseDirectory, fixture.Path)
		fixtureBytes, readErr := os.ReadFile(path)
		fixtureDigest := sha256.Sum256(fixtureBytes)
		if pathErr != nil || readErr != nil || !strings.EqualFold(fixture.SHA256, hex.EncodeToString(fixtureDigest[:])) {
			report.Errors = append(report.Errors, "fixture hash mismatch: "+fixture.Name)
		}
		if _, duplicate := seenFixtures[fixture.Name]; duplicate {
			report.Errors = append(report.Errors, "duplicate fixture: "+fixture.Name)
		}
		seenFixtures[fixture.Name] = struct{}{}
	}
	if len(manifest.Fixtures) == 0 {
		report.Errors = append(report.Errors, "at least one fixture provenance record is required")
	}
	sort.Slice(report.Slices, func(i, j int) bool { return report.Slices[i].Name < report.Slices[j].Name })
	sort.Slice(report.Fixtures, func(i, j int) bool { return report.Fixtures[i].Name < report.Fixtures[j].Name })
	report.HumanReviewed = report.ReviewedCases == report.TotalCases && report.PendingCases == 0 && report.RejectedCases == 0
	report.Passed = len(report.Errors) == 0
	report.BaselineEligible = report.Passed && report.HumanReviewed
	return report
}
