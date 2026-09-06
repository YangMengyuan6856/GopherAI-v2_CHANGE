package evaluation

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReviewManifestBindsPendingLabelsAndFixtures(t *testing.T) {
	base := filepath.Join("..", "..", "evals")
	report, err := ValidateReviewManifestFile(filepath.Join(base, "devsupport-eval-v1.review.json"), filepath.Join(base, "devsupport-eval-v1.manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || !report.CatalogMatched || report.TotalCases != 320 || report.PendingCases != 320 || report.ReviewedCases != 0 || report.HumanReviewed || report.BaselineEligible || len(report.Slices) != 6 || len(report.Fixtures) != 3 {
		t.Fatalf("unexpected review manifest report: %+v", report)
	}
}

func TestReviewManifestCannotForgeSliceLevelApproval(t *testing.T) {
	base := filepath.Join("..", "..", "evals")
	reviewBytes, err := os.ReadFile(filepath.Join(base, "devsupport-eval-v1.review.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := decodeReviewManifest(reviewBytes)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 6, 5, 0, 0, 0, time.UTC)
	manifest.Slices[0].ReviewStatus, manifest.Slices[0].Reviewer, manifest.Slices[0].ReviewedAt = "reviewed", "project_owner", &now
	forged, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	catalogBytes, _ := os.ReadFile(filepath.Join(base, "devsupport-eval-v1.manifest.json"))
	catalog, _, err := LoadEvalCatalogManifest(bytes.NewReader(catalogBytes))
	if err != nil {
		t.Fatal(err)
	}
	catalogReport := ValidateEvalCatalog(catalog, catalogBytes, base)
	report := validateReviewManifest(manifest, forged, base, catalog, catalogReport)
	if report.Passed || report.BaselineEligible || report.ReviewedCases != 150 {
		t.Fatalf("forged review unexpectedly passed: %+v", report)
	}
}

func TestReviewManifestRejectsFixtureHashDrift(t *testing.T) {
	base := filepath.Join("..", "..", "evals")
	reviewBytes, _ := os.ReadFile(filepath.Join(base, "devsupport-eval-v1.review.json"))
	manifest, err := decodeReviewManifest(reviewBytes)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Fixtures[0].SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	drifted, _ := json.Marshal(manifest)
	catalogBytes, _ := os.ReadFile(filepath.Join(base, "devsupport-eval-v1.manifest.json"))
	catalog, _, _ := LoadEvalCatalogManifest(bytes.NewReader(catalogBytes))
	catalogReport := ValidateEvalCatalog(catalog, catalogBytes, base)
	report := validateReviewManifest(manifest, drifted, base, catalog, catalogReport)
	if report.Passed || report.BaselineEligible {
		t.Fatalf("fixture drift unexpectedly passed: %+v", report)
	}
}
