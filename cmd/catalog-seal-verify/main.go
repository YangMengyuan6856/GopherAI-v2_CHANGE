package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"GopherAI/internal/catalogseal"
)

func main() {
	artifact := flag.String("artifact", "", "path to an immutable Full 320 sealed candidate directory")
	flag.Parse()
	if strings.TrimSpace(*artifact) == "" {
		fatal(errors.New("artifact is required"))
	}
	root, err := filepath.Abs(filepath.Clean(*artifact))
	if err != nil {
		fatal(errors.New("artifact path is invalid"))
	}
	report, err := catalogseal.ValidateArtifact(root)
	if err != nil {
		fatal(err)
	}
	result := map[string]any{
		"schema_version":                report.SchemaVersion,
		"validated":                     true,
		"seal_id":                       report.SealID,
		"seal_sha256":                   report.SealSHA256,
		"dataset_version":               report.DatasetVersion,
		"case_count":                    report.CaseCount,
		"approved_cases":                report.ApprovedCases,
		"output_catalog_sha256":         report.OutputCatalogSHA256,
		"output_review_manifest_sha256": report.OutputReviewSHA256,
		"next_required_gate":            report.NextRequiredGate,
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		fatal(errors.New("encode validation result"))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "catalog sealed candidate validation failed:", err)
	os.Exit(1)
}
