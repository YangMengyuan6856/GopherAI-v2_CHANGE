package evaluation

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const EvalCatalogSchemaVersion = "devsupport-eval-catalog-v1"

var (
	credentialSignatures = []*regexp.Regexp{
		regexp.MustCompile(`(?i)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`),
		regexp.MustCompile(`\bLTAI[A-Za-z0-9]{12,}\b`),
		regexp.MustCompile(`\bAKIA[A-Z0-9]{16}\b`),
		regexp.MustCompile(`(?i)\bBearer\s+eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
		regexp.MustCompile(`(?i)\b[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}\b`),
	}
	sensitiveFieldName = regexp.MustCompile(`(?i)(password|passwd|api_?key|access_?key|secret_?key|smtp_?code|authorization_?code)`)
)

type EvalCatalogManifest struct {
	SchemaVersion  string                     `json:"schema_version"`
	DatasetVersion string                     `json:"dataset_version"`
	TotalCases     int                        `json:"total_cases"`
	Slices         []EvalCatalogSliceManifest `json:"slices"`
}

type EvalCatalogSliceManifest struct {
	Name            string   `json:"name"`
	Path            string   `json:"path"`
	ExpectedCount   int      `json:"expected_count"`
	SHA256          string   `json:"sha256"`
	DatasetVersions []string `json:"dataset_versions"`
	ReviewPolicy    string   `json:"review_policy"`
}

type EvalCatalogSliceResult struct {
	Name          string         `json:"name"`
	Path          string         `json:"path"`
	ExpectedCount int            `json:"expected_count"`
	ActualCount   int            `json:"actual_count"`
	ExpectedSHA   string         `json:"expected_sha256"`
	ActualSHA     string         `json:"actual_sha256"`
	ReviewCounts  map[string]int `json:"review_counts"`
	Passed        bool           `json:"passed"`
	Errors        []string       `json:"errors,omitempty"`
}

type EvalCatalogValidationReport struct {
	SchemaVersion  string                   `json:"schema_version"`
	DatasetVersion string                   `json:"dataset_version"`
	ManifestSHA256 string                   `json:"manifest_sha256"`
	ExpectedTotal  int                      `json:"expected_total"`
	ActualTotal    int                      `json:"actual_total"`
	UniqueIDs      int                      `json:"unique_ids"`
	SensitiveHits  int                      `json:"sensitive_hits"`
	Passed         bool                     `json:"passed"`
	Slices         []EvalCatalogSliceResult `json:"slices"`
	Errors         []string                 `json:"errors,omitempty"`
}

type evalCatalogCaseHeader struct {
	ID             string `json:"id"`
	ReviewedBy     string `json:"reviewed_by"`
	DatasetVersion string `json:"dataset_version"`
}

func LoadEvalCatalogManifest(reader io.Reader) (EvalCatalogManifest, []byte, error) {
	if reader == nil {
		return EvalCatalogManifest{}, nil, errors.New("evaluation catalog manifest reader is required")
	}
	encoded, err := io.ReadAll(io.LimitReader(reader, 1<<20))
	if err != nil {
		return EvalCatalogManifest{}, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var manifest EvalCatalogManifest
	if err := decoder.Decode(&manifest); err != nil {
		return EvalCatalogManifest{}, nil, fmt.Errorf("decode evaluation catalog manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return EvalCatalogManifest{}, nil, errors.New("evaluation catalog manifest has trailing content")
	}
	if err := validateEvalCatalogManifest(manifest); err != nil {
		return EvalCatalogManifest{}, nil, err
	}
	return manifest, encoded, nil
}

func ValidateEvalCatalog(manifest EvalCatalogManifest, manifestBytes []byte, baseDirectory string) EvalCatalogValidationReport {
	report := EvalCatalogValidationReport{
		SchemaVersion: EvalCatalogSchemaVersion, DatasetVersion: manifest.DatasetVersion,
		ExpectedTotal: manifest.TotalCases, Slices: make([]EvalCatalogSliceResult, 0, len(manifest.Slices)),
	}
	manifestDigest := sha256.Sum256(manifestBytes)
	report.ManifestSHA256 = hex.EncodeToString(manifestDigest[:])
	globalIDs := make(map[string]string, manifest.TotalCases)
	for _, slice := range manifest.Slices {
		result, ids, sensitiveHits := validateEvalCatalogSlice(slice, baseDirectory)
		report.ActualTotal += result.ActualCount
		report.SensitiveHits += sensitiveHits
		for _, id := range ids {
			if previous, duplicate := globalIDs[id]; duplicate {
				result.Errors = append(result.Errors, fmt.Sprintf("duplicate case id %s already declared in %s", id, previous))
				result.Passed = false
			} else {
				globalIDs[id] = slice.Name
			}
		}
		report.Slices = append(report.Slices, result)
	}
	report.UniqueIDs = len(globalIDs)
	if report.ActualTotal != report.ExpectedTotal {
		report.Errors = append(report.Errors, fmt.Sprintf("catalog total mismatch: got %d want %d", report.ActualTotal, report.ExpectedTotal))
	}
	if report.UniqueIDs != report.ActualTotal {
		report.Errors = append(report.Errors, "case ids are not globally unique")
	}
	if report.SensitiveHits != 0 {
		report.Errors = append(report.Errors, fmt.Sprintf("credential-like values found: %d", report.SensitiveHits))
	}
	for _, slice := range report.Slices {
		if !slice.Passed {
			report.Errors = append(report.Errors, "slice failed: "+slice.Name)
		}
	}
	report.Passed = len(report.Errors) == 0
	return report
}

func ValidateEvalCatalogFile(manifestPath string) (EvalCatalogValidationReport, error) {
	file, err := os.Open(manifestPath)
	if err != nil {
		return EvalCatalogValidationReport{}, err
	}
	defer file.Close()
	manifest, encoded, err := LoadEvalCatalogManifest(file)
	if err != nil {
		return EvalCatalogValidationReport{}, err
	}
	return ValidateEvalCatalog(manifest, encoded, filepath.Dir(manifestPath)), nil
}

func validateEvalCatalogSlice(slice EvalCatalogSliceManifest, baseDirectory string) (EvalCatalogSliceResult, []string, int) {
	result := EvalCatalogSliceResult{
		Name: slice.Name, Path: slice.Path, ExpectedCount: slice.ExpectedCount, ExpectedSHA: strings.ToLower(slice.SHA256),
		ReviewCounts: map[string]int{}, Passed: true,
	}
	resolvedPath, err := safeCatalogPath(baseDirectory, slice.Path)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		result.Passed = false
		return result, nil, 0
	}
	file, err := os.Open(resolvedPath)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		result.Passed = false
		return result, nil, 0
	}
	defer file.Close()
	hasher := sha256.New()
	tee := io.TeeReader(file, hasher)
	scanner := bufio.NewScanner(tee)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	ids := []string{}
	allowedVersions := stringSet(slice.DatasetVersions)
	sensitiveHits := 0
	line := 0
	for scanner.Scan() {
		line++
		text := bytes.TrimSpace(scanner.Bytes())
		if len(text) == 0 {
			continue
		}
		var header evalCatalogCaseHeader
		if err := json.Unmarshal(text, &header); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("line %d invalid JSON: %v", line, err))
			continue
		}
		header.ID, header.ReviewedBy, header.DatasetVersion = strings.TrimSpace(header.ID), strings.TrimSpace(header.ReviewedBy), strings.TrimSpace(header.DatasetVersion)
		if header.ID == "" || header.DatasetVersion == "" || (header.ReviewedBy != "human" && header.ReviewedBy != "pending_user") {
			result.Errors = append(result.Errors, fmt.Sprintf("line %d missing id/version or invalid review state", line))
			continue
		}
		if _, exists := allowedVersions[header.DatasetVersion]; !exists {
			result.Errors = append(result.Errors, fmt.Sprintf("line %d uses undeclared dataset version %s", line, header.DatasetVersion))
		}
		result.ReviewCounts[header.ReviewedBy]++
		ids = append(ids, header.ID)
		sensitiveHits += countCredentialLikeValues(text)
	}
	if err := scanner.Err(); err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
	result.ActualCount = len(ids)
	result.ActualSHA = hex.EncodeToString(hasher.Sum(nil))
	if result.ActualCount != slice.ExpectedCount {
		result.Errors = append(result.Errors, fmt.Sprintf("case count mismatch: got %d want %d", result.ActualCount, slice.ExpectedCount))
	}
	if !strings.EqualFold(result.ActualSHA, slice.SHA256) {
		result.Errors = append(result.Errors, "sha256 mismatch")
	}
	if slice.ReviewPolicy == "human_only" && result.ReviewCounts["pending_user"] != 0 {
		result.Errors = append(result.Errors, "review policy requires all labels to be human-reviewed")
	}
	if slice.ReviewPolicy == "explicit" && result.ReviewCounts["human"]+result.ReviewCounts["pending_user"] != result.ActualCount {
		result.Errors = append(result.Errors, "review policy requires an explicit review state")
	}
	result.Passed = len(result.Errors) == 0
	return result, ids, sensitiveHits
}

func validateEvalCatalogManifest(manifest EvalCatalogManifest) error {
	if manifest.SchemaVersion != EvalCatalogSchemaVersion || strings.TrimSpace(manifest.DatasetVersion) == "" || manifest.TotalCases < 1 || len(manifest.Slices) == 0 {
		return errors.New("evaluation catalog schema, version, total and slices are required")
	}
	seen := make(map[string]struct{}, len(manifest.Slices))
	total := 0
	for _, slice := range manifest.Slices {
		if strings.TrimSpace(slice.Name) == "" || strings.TrimSpace(slice.Path) == "" || slice.ExpectedCount < 1 || len(slice.SHA256) != 64 || len(slice.DatasetVersions) == 0 {
			return fmt.Errorf("evaluation catalog slice %q is incomplete", slice.Name)
		}
		if _, err := hex.DecodeString(slice.SHA256); err != nil {
			return fmt.Errorf("evaluation catalog slice %s has invalid sha256", slice.Name)
		}
		if slice.ReviewPolicy != "explicit" && slice.ReviewPolicy != "human_only" {
			return fmt.Errorf("evaluation catalog slice %s has invalid review policy", slice.Name)
		}
		if _, duplicate := seen[slice.Name]; duplicate {
			return fmt.Errorf("duplicate evaluation catalog slice %s", slice.Name)
		}
		seen[slice.Name] = struct{}{}
		total += slice.ExpectedCount
	}
	if total != manifest.TotalCases {
		return fmt.Errorf("manifest slice counts sum to %d, want %d", total, manifest.TotalCases)
	}
	return nil
}

func safeCatalogPath(baseDirectory string, relativePath string) (string, error) {
	base, err := filepath.Abs(baseDirectory)
	if err != nil {
		return "", err
	}
	candidate, err := filepath.Abs(filepath.Join(base, filepath.Clean(relativePath)))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(base, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("catalog slice path escapes manifest directory")
	}
	return candidate, nil
}

func countCredentialLikeValues(encoded []byte) int {
	hits := 0
	for _, pattern := range credentialSignatures {
		hits += len(pattern.FindAll(encoded, -1))
	}
	var value any
	if json.Unmarshal(encoded, &value) == nil {
		hits += countSensitiveFields(value)
	}
	return hits
}

func countSensitiveFields(value any) int {
	switch typed := value.(type) {
	case map[string]any:
		hits := 0
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := typed[key]
			if sensitiveFieldName.MatchString(key) {
				if text, ok := child.(string); ok && !safePlaceholder(text) {
					hits++
				}
			}
			hits += countSensitiveFields(child)
		}
		return hits
	case []any:
		hits := 0
		for _, child := range typed {
			hits += countSensitiveFields(child)
		}
		return hits
	default:
		return 0
	}
}

func safePlaceholder(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, marker := range []string{"redacted", "placeholder", "example", "forbidden", "unknown", "not-provided", "已脱敏", "不提供", "未知"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return value == ""
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[strings.TrimSpace(value)] = struct{}{}
	}
	return result
}
