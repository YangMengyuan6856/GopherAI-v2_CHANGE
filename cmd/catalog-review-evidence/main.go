package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"GopherAI/internal/catalogreview"
)

const maxEvidenceBytes = 4 << 20

func main() {
	input := flag.String("input", "", "path to a downloaded Full 320 review evidence JSON file")
	manifest := flag.String("manifest", catalogreview.DefaultManifestPath, "path to the immutable evaluation catalog manifest")
	flag.Parse()
	if strings.TrimSpace(*input) == "" || strings.TrimSpace(*manifest) == "" {
		fatal(errors.New("input and manifest are required"))
	}
	encoded, err := os.ReadFile(*input)
	if err != nil || len(encoded) == 0 || len(encoded) > maxEvidenceBytes {
		fatal(errors.New("review evidence file is unavailable or too large"))
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	report := catalogreview.EvidenceSnapshot{}
	if err := decoder.Decode(&report); err != nil {
		fatal(fmt.Errorf("decode review evidence: %w", err))
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		fatal(errors.New("review evidence has trailing content"))
	}
	snapshot, err := catalogreview.NewFileArtifactStore(*manifest).Load()
	if err != nil {
		fatal(errors.New("catalog artifact is unavailable or invalid"))
	}
	if err := catalogreview.ValidateEvidenceAgainstSnapshot(report, snapshot); err != nil {
		fatal(err)
	}
	result := map[string]any{
		"schema_version": report.SchemaVersion, "validated": true, "dataset_version": report.DatasetVersion,
		"catalog_sha256": report.CatalogSHA256, "snapshot_sha256": report.SnapshotSHA256,
		"reviewed_cases": report.ReviewedCases, "total_cases": report.TotalCases,
		"approved_cases": report.ApprovedCases, "rejected_cases": report.RejectedCases,
		"ready_for_sealed_materialization": report.ReadyForSealedMaterialization,
	}
	output, _ := json.Marshal(result)
	fmt.Println(string(output))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "catalog review evidence validation failed:", err)
	os.Exit(1)
}
