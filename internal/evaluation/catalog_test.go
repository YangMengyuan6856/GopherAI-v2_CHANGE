package evaluation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateEvalCatalogChecksHashCountReviewAndGlobalIDs(t *testing.T) {
	directory := t.TempDir()
	content := []byte("{\"id\":\"case-1\",\"reviewed_by\":\"pending_user\",\"dataset_version\":\"slice-v1\"}\n{\"id\":\"case-2\",\"reviewed_by\":\"human\",\"dataset_version\":\"slice-v1\"}\n")
	if err := os.WriteFile(filepath.Join(directory, "slice.jsonl"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	manifest := EvalCatalogManifest{
		SchemaVersion: EvalCatalogSchemaVersion, DatasetVersion: "catalog-v1", TotalCases: 2,
		Slices: []EvalCatalogSliceManifest{{
			Name: "slice", Path: "slice.jsonl", ExpectedCount: 2, SHA256: hex.EncodeToString(digest[:]),
			DatasetVersions: []string{"slice-v1"}, ReviewPolicy: "explicit",
		}},
	}
	manifestBytes, _ := json.Marshal(manifest)
	report := ValidateEvalCatalog(manifest, manifestBytes, directory)
	if !report.Passed || report.ActualTotal != 2 || report.UniqueIDs != 2 || report.Slices[0].ReviewCounts["pending_user"] != 1 {
		t.Fatalf("valid catalog rejected: %+v", report)
	}

	manifest.Slices[0].SHA256 = string(make([]byte, 64))
	report = ValidateEvalCatalog(manifest, manifestBytes, directory)
	if report.Passed || report.Slices[0].Passed {
		t.Fatalf("hash mismatch must fail: %+v", report)
	}
}

func TestValidateEvalCatalogRejectsCredentialLikeValue(t *testing.T) {
	directory := t.TempDir()
	content := []byte("{\"id\":\"case-1\",\"api_key\":\"real-looking-value\",\"reviewed_by\":\"pending_user\",\"dataset_version\":\"slice-v1\"}\n")
	if err := os.WriteFile(filepath.Join(directory, "slice.jsonl"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	manifest := EvalCatalogManifest{
		SchemaVersion: EvalCatalogSchemaVersion, DatasetVersion: "catalog-v1", TotalCases: 1,
		Slices: []EvalCatalogSliceManifest{{Name: "slice", Path: "slice.jsonl", ExpectedCount: 1, SHA256: hex.EncodeToString(digest[:]), DatasetVersions: []string{"slice-v1"}, ReviewPolicy: "explicit"}},
	}
	report := ValidateEvalCatalog(manifest, []byte("manifest"), directory)
	if report.Passed || report.SensitiveHits != 1 {
		t.Fatalf("credential-like field must fail: %+v", report)
	}
}

func TestSafeCatalogPathRejectsEscape(t *testing.T) {
	if _, err := safeCatalogPath(t.TempDir(), "../outside.jsonl"); err == nil {
		t.Fatal("manifest path traversal must fail")
	}
}

func TestLoadEvalCatalogManifestRejectsCountDrift(t *testing.T) {
	encoded := `{"schema_version":"devsupport-eval-catalog-v1","dataset_version":"catalog-v1","total_cases":2,"slices":[{"name":"one","path":"one.jsonl","expected_count":1,"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","dataset_versions":["one-v1"],"review_policy":"explicit"}]}`
	if _, _, err := LoadEvalCatalogManifest(strings.NewReader(encoded)); err == nil {
		t.Fatal("manifest total drift must fail")
	}
}
