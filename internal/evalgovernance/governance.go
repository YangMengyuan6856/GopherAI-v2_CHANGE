package evalgovernance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	SchemaVersion = "evaluation-dataset-governance-v1"
	DefaultPath   = "evals/devsupport-eval-v1.governance.json"
)

var ErrInvalidGovernance = errors.New("evaluation dataset governance is invalid")

type DatasetCard struct {
	Title           string   `json:"title"`
	SourceCategory  string   `json:"source_category"`
	TargetSystem    string   `json:"target_system"`
	Purpose         string   `json:"purpose"`
	AllowedClaim    string   `json:"allowed_claim"`
	ForbiddenClaims []string `json:"forbidden_claims"`
	ReviewPrinciple string   `json:"review_principle"`
}

type TruthClass struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Meaning string `json:"meaning"`
}

type SourceRef struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Title  string `json:"title"`
}

type SliceCard struct {
	Name             string      `json:"name"`
	Title            string      `json:"title"`
	ScenarioScope    string      `json:"scenario_scope"`
	TruthType        string      `json:"truth_type"`
	EvaluationStage  string      `json:"evaluation_stage"`
	Purpose          string      `json:"purpose"`
	ReviewQuestion   string      `json:"review_question"`
	PassCriteria     []string    `json:"pass_criteria"`
	RejectCriteria   []string    `json:"reject_criteria"`
	SourceReferences []SourceRef `json:"source_refs"`
}

type JudgeCalibrationCard struct {
	Title            string           `json:"title"`
	DatasetVersion   string           `json:"dataset_version"`
	DatasetSHA256    string           `json:"dataset_sha256"`
	ScenarioScope    string           `json:"scenario_scope"`
	TruthType        string           `json:"truth_type"`
	Purpose          string           `json:"purpose"`
	ReviewPrinciple  string           `json:"review_principle"`
	VariantOrder     []string         `json:"variant_order"`
	ScoreAnchors     []ScoreAnchor    `json:"score_anchors"`
	Dimensions       []ScoreDimension `json:"dimensions"`
	SourceReferences []SourceRef      `json:"source_refs"`
	Limitations      []string         `json:"limitations"`
}

type ScoreAnchor struct {
	Value   float64 `json:"value"`
	Label   string  `json:"label"`
	Meaning string  `json:"meaning"`
}

type ScoreDimension struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Question string `json:"question"`
}

type Manifest struct {
	SchemaVersion     string               `json:"schema_version"`
	GovernanceVersion string               `json:"governance_version"`
	DatasetVersion    string               `json:"dataset_version"`
	CatalogSHA256     string               `json:"catalog_sha256"`
	DatasetCard       DatasetCard          `json:"dataset_card"`
	TruthClasses      []TruthClass         `json:"truth_classes"`
	Slices            []SliceCard          `json:"slices"`
	JudgeCalibration  JudgeCalibrationCard `json:"judge_calibration"`
	ManifestSHA256    string               `json:"manifest_sha256"`
}

func LoadFile(path, expectedDatasetVersion, expectedCatalogSHA256 string) (Manifest, error) {
	path = strings.TrimSpace(path)
	if path == "" || strings.TrimSpace(expectedDatasetVersion) == "" || len(strings.TrimSpace(expectedCatalogSHA256)) != 64 {
		return Manifest{}, ErrInvalidGovernance
	}
	encoded, err := os.ReadFile(path)
	if err != nil || len(encoded) == 0 || len(encoded) > 2<<20 {
		return Manifest{}, ErrInvalidGovernance
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, ErrInvalidGovernance
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, ErrInvalidGovernance
	}
	digest := sha256.Sum256(encoded)
	manifest.ManifestSHA256 = hex.EncodeToString(digest[:])
	if manifest.DatasetVersion != strings.TrimSpace(expectedDatasetVersion) || manifest.CatalogSHA256 != strings.ToLower(strings.TrimSpace(expectedCatalogSHA256)) {
		return Manifest{}, ErrInvalidGovernance
	}
	if err := validate(manifest); err != nil {
		return Manifest{}, err
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Manifest{}, ErrInvalidGovernance
	}
	for _, source := range allSources(manifest) {
		resolved, err := safePath(base, source.Path)
		if err != nil {
			return Manifest{}, ErrInvalidGovernance
		}
		content, err := os.ReadFile(resolved)
		if err != nil {
			return Manifest{}, ErrInvalidGovernance
		}
		sum := sha256.Sum256(content)
		if source.SHA256 != hex.EncodeToString(sum[:]) {
			return Manifest{}, ErrInvalidGovernance
		}
	}
	return manifest, nil
}

func LoadBoundCatalog(path, catalogPath string) (Manifest, error) {
	catalogPath = strings.TrimSpace(catalogPath)
	if catalogPath == "" {
		return Manifest{}, ErrInvalidGovernance
	}
	encoded, err := os.ReadFile(catalogPath)
	if err != nil || len(encoded) == 0 || len(encoded) > 2<<20 {
		return Manifest{}, ErrInvalidGovernance
	}
	var identity struct {
		DatasetVersion string `json:"dataset_version"`
	}
	if err := json.Unmarshal(encoded, &identity); err != nil || strings.TrimSpace(identity.DatasetVersion) == "" {
		return Manifest{}, ErrInvalidGovernance
	}
	digest := sha256.Sum256(encoded)
	return LoadFile(path, identity.DatasetVersion, hex.EncodeToString(digest[:]))
}

func (manifest Manifest) Slice(name string) (SliceCard, bool) {
	for _, item := range manifest.Slices {
		if item.Name == strings.TrimSpace(name) {
			return item, true
		}
	}
	return SliceCard{}, false
}

func (manifest Manifest) TruthClass(id string) (TruthClass, bool) {
	for _, item := range manifest.TruthClasses {
		if item.ID == strings.TrimSpace(id) {
			return item, true
		}
	}
	return TruthClass{}, false
}

func validate(manifest Manifest) error {
	if manifest.SchemaVersion != SchemaVersion || strings.TrimSpace(manifest.GovernanceVersion) == "" || strings.TrimSpace(manifest.DatasetVersion) == "" || len(manifest.CatalogSHA256) != 64 ||
		blank(manifest.DatasetCard.Title, manifest.DatasetCard.SourceCategory, manifest.DatasetCard.TargetSystem, manifest.DatasetCard.Purpose, manifest.DatasetCard.AllowedClaim, manifest.DatasetCard.ReviewPrinciple) || len(manifest.DatasetCard.ForbiddenClaims) == 0 {
		return ErrInvalidGovernance
	}
	truth := make(map[string]struct{}, len(manifest.TruthClasses))
	for _, item := range manifest.TruthClasses {
		if blank(item.ID, item.Title, item.Meaning) {
			return ErrInvalidGovernance
		}
		if _, duplicate := truth[item.ID]; duplicate {
			return ErrInvalidGovernance
		}
		truth[item.ID] = struct{}{}
	}
	required := []string{"diagnosis", "insufficient_evidence", "intent", "memory", "rag", "tool"}
	actual := make([]string, 0, len(manifest.Slices))
	seen := map[string]struct{}{}
	for _, item := range manifest.Slices {
		if blank(item.Name, item.Title, item.ScenarioScope, item.TruthType, item.EvaluationStage, item.Purpose, item.ReviewQuestion) || len(item.PassCriteria) == 0 || len(item.RejectCriteria) == 0 || len(item.SourceReferences) == 0 {
			return ErrInvalidGovernance
		}
		if _, exists := truth[item.TruthType]; !exists {
			return ErrInvalidGovernance
		}
		if _, duplicate := seen[item.Name]; duplicate {
			return ErrInvalidGovernance
		}
		seen[item.Name] = struct{}{}
		actual = append(actual, item.Name)
		if !validSources(item.SourceReferences) {
			return ErrInvalidGovernance
		}
	}
	sort.Strings(actual)
	if strings.Join(actual, "\x00") != strings.Join(required, "\x00") {
		return ErrInvalidGovernance
	}
	judge := manifest.JudgeCalibration
	if blank(judge.Title, judge.DatasetVersion, judge.ScenarioScope, judge.TruthType, judge.Purpose, judge.ReviewPrinciple) || len(judge.DatasetSHA256) != 64 || len(judge.VariantOrder) != 5 || len(judge.ScoreAnchors) != 5 || len(judge.Dimensions) != 5 || len(judge.SourceReferences) == 0 || len(judge.Limitations) == 0 || !validSources(judge.SourceReferences) {
		return ErrInvalidGovernance
	}
	if _, exists := truth[judge.TruthType]; !exists {
		return ErrInvalidGovernance
	}
	for index, anchor := range judge.ScoreAnchors {
		if anchor.Value != float64(index)*.25 || blank(anchor.Label, anchor.Meaning) {
			return ErrInvalidGovernance
		}
	}
	dimensions := map[string]struct{}{}
	for _, dimension := range judge.Dimensions {
		if blank(dimension.ID, dimension.Title, dimension.Question) {
			return ErrInvalidGovernance
		}
		dimensions[dimension.ID] = struct{}{}
	}
	for _, required := range []string{"relevance", "completeness", "helpfulness", "groundedness", "safety"} {
		if _, exists := dimensions[required]; !exists {
			return ErrInvalidGovernance
		}
	}
	return nil
}

func validSources(values []SourceRef) bool {
	seen := map[string]struct{}{}
	for _, item := range values {
		if blank(item.Kind, item.Path, item.Title) || len(item.SHA256) != 64 || item.SHA256 != strings.ToLower(item.SHA256) {
			return false
		}
		if _, duplicate := seen[item.Path]; duplicate {
			return false
		}
		seen[item.Path] = struct{}{}
	}
	return true
}

func allSources(manifest Manifest) []SourceRef {
	unique := map[string]SourceRef{}
	for _, slice := range manifest.Slices {
		for _, source := range slice.SourceReferences {
			unique[source.Path] = source
		}
	}
	for _, source := range manifest.JudgeCalibration.SourceReferences {
		unique[source.Path] = source
	}
	result := make([]SourceRef, 0, len(unique))
	for _, source := range unique {
		result = append(result, source)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

func safePath(base, relative string) (string, error) {
	if filepath.IsAbs(relative) || strings.TrimSpace(relative) == "" {
		return "", ErrInvalidGovernance
	}
	candidate, err := filepath.Abs(filepath.Join(base, filepath.Clean(relative)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrInvalidGovernance
	}
	return candidate, nil
}

func blank(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}
