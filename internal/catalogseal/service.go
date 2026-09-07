package catalogseal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"GopherAI/internal/catalogreview"
	"GopherAI/internal/evaluation"
)

const (
	SchemaVersion             = "evaluation-catalog-seal-v1"
	StatusSchemaVersion       = "evaluation-catalog-seal-status-v2"
	DefaultCatalogPath        = "evals/devsupport-eval-v1.manifest.json"
	DefaultReviewManifestPath = "evals/devsupport-eval-v1.review.json"
	DefaultOutputRoot         = "/root/GopherAI_Runtime/evaluation/catalog-review-seals"
	Acknowledgment            = "I_CONFIRM_320_APPROVED_AND_REQUEST_SEALED_CANDIDATE"
	sealedCatalogName         = "devsupport-eval-v1.manifest.json"
	sealedReviewName          = "devsupport-eval-v1.review.json"
)

var (
	ErrUnavailable      = errors.New("evaluation catalog seal is unavailable")
	ErrReviewIncomplete = errors.New("evaluation catalog review is incomplete")
	ErrStaleReview      = errors.New("evaluation catalog review commitment is stale")
	ErrInvalidCommand   = errors.New("evaluation catalog seal command is invalid")
	ErrInvalidArtifact  = errors.New("evaluation catalog sealed artifact is invalid")
)

type ReviewSource interface {
	SealSnapshot(context.Context, string) (catalogreview.Snapshot, catalogreview.Progress, error)
}

type FileEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type Report struct {
	SchemaVersion              string      `json:"schema_version"`
	SealSHA256                 string      `json:"seal_sha256"`
	SealID                     string      `json:"seal_id"`
	Status                     string      `json:"status"`
	CreatedAt                  time.Time   `json:"created_at"`
	DatasetVersion             string      `json:"dataset_version"`
	SourceCatalogSHA256        string      `json:"source_catalog_sha256"`
	SourceReviewManifestSHA256 string      `json:"source_review_manifest_sha256"`
	SourceReviewSetSHA256      string      `json:"source_review_set_sha256"`
	ReviewerHash               string      `json:"reviewer_hash"`
	CaseCount                  int         `json:"case_count"`
	ApprovedCases              int         `json:"approved_cases"`
	OutputCatalogSHA256        string      `json:"output_catalog_sha256"`
	OutputReviewSHA256         string      `json:"output_review_manifest_sha256"`
	Files                      []FileEntry `json:"files"`
	NextRequiredGate           string      `json:"next_required_gate"`
	Guardrails                 []string    `json:"guardrails"`
}

type Receipt struct {
	Created bool   `json:"created"`
	Reused  bool   `json:"reused"`
	Report  Report `json:"report"`
}

type Status struct {
	SchemaVersion  string                 `json:"schema_version"`
	Status         string                 `json:"status"`
	Eligible       bool                   `json:"eligible"`
	DatasetVersion string                 `json:"dataset_version"`
	CatalogSHA256  string                 `json:"catalog_sha256"`
	ReviewSetSHA   string                 `json:"review_set_sha256"`
	Progress       catalogreview.Progress `json:"progress"`
	CurrentSeal    *Report                `json:"current_seal,omitempty"`
	NextGate       string                 `json:"next_gate"`
	Guardrails     []string               `json:"guardrails"`
}

type Command struct {
	CatalogSHA256   string
	ReviewSetSHA256 string
	Acknowledgment  string
}

type Service struct {
	reviews            ReviewSource
	catalogPath        string
	reviewManifestPath string
	outputRoot         string
	clock              func() time.Time
}

func NewService(reviews ReviewSource, catalogPath, reviewManifestPath, outputRoot string, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{reviews: reviews, catalogPath: catalogPath, reviewManifestPath: reviewManifestPath, outputRoot: outputRoot, clock: clock}
}

func (service *Service) Status(ctx context.Context, reviewer string) (Status, error) {
	snapshot, progress, reviewerHash, err := service.snapshot(ctx, reviewer)
	if err != nil {
		return Status{}, err
	}
	result := Status{
		SchemaVersion: StatusSchemaVersion, Status: "blocked_human_review", Eligible: progress.ReadyForMaterializing,
		DatasetVersion: snapshot.DatasetVersion, CatalogSHA256: snapshot.CatalogSHA256, ReviewSetSHA: progress.ReviewSetSHA256, Progress: progress,
		NextGate:   "全部 320 条必须由当前登录复核人逐例 approved，且退回数为 0。",
		Guardrails: []string{"server_side_database_source_only", "no_exported_snapshot_as_authority", "all_320_approved_required", "immutable_create_only_artifact", "no_baseline_pointer_write", "no_policy_write"},
	}
	if progress.ReadyForMaterializing {
		result.Status, result.NextGate = "ready_for_seal", "创建不可变 reviewed 候选目录；随后必须从该目录重跑五类评测与统一报告。"
	}
	sealPath := filepath.Join(service.outputRoot, sealID(snapshot.CatalogSHA256, progress.ReviewSetSHA256, reviewerHash))
	if report, loadErr := ValidateArtifact(sealPath); loadErr == nil {
		if report.SourceCatalogSHA256 == snapshot.CatalogSHA256 && report.SourceReviewSetSHA256 == progress.ReviewSetSHA256 && report.ReviewerHash == reviewerHash {
			result.Status, result.CurrentSeal = "sealed_candidate_ready", &report
			result.NextGate = report.NextRequiredGate
		}
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return Status{}, loadErr
	}
	return result, nil
}

func (service *Service) Seal(ctx context.Context, reviewer string, command Command) (Receipt, error) {
	snapshot, progress, reviewerHash, err := service.snapshot(ctx, reviewer)
	if err != nil {
		return Receipt{}, err
	}
	if command.Acknowledgment != Acknowledgment {
		return Receipt{}, ErrInvalidCommand
	}
	if strings.TrimSpace(command.CatalogSHA256) != snapshot.CatalogSHA256 || strings.TrimSpace(command.ReviewSetSHA256) != progress.ReviewSetSHA256 {
		return Receipt{}, ErrStaleReview
	}
	if !progress.ReadyForMaterializing || progress.Total != 320 || progress.Approved != progress.Total || progress.Rejected != 0 || progress.Pending != 0 {
		return Receipt{}, ErrReviewIncomplete
	}
	if err := os.MkdirAll(service.outputRoot, 0o750); err != nil {
		return Receipt{}, ErrUnavailable
	}
	id := sealID(snapshot.CatalogSHA256, progress.ReviewSetSHA256, reviewerHash)
	target := filepath.Join(service.outputRoot, id)
	if report, loadErr := ValidateArtifact(target); loadErr == nil {
		if report.SourceCatalogSHA256 != snapshot.CatalogSHA256 || report.SourceReviewSetSHA256 != progress.ReviewSetSHA256 || report.ReviewerHash != reviewerHash {
			return Receipt{}, ErrInvalidArtifact
		}
		return Receipt{Reused: true, Report: report}, nil
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return Receipt{}, loadErr
	}
	temporary, err := os.MkdirTemp(service.outputRoot, ".seal-")
	if err != nil {
		return Receipt{}, ErrUnavailable
	}
	defer os.RemoveAll(temporary)
	report, err := service.materialize(temporary, id, reviewerHash, snapshot, progress)
	if err != nil {
		return Receipt{}, err
	}
	if err := os.Rename(temporary, target); err != nil {
		if existing, loadErr := ValidateArtifact(target); loadErr == nil && existing.SealID == id {
			return Receipt{Reused: true, Report: existing}, nil
		}
		return Receipt{}, ErrUnavailable
	}
	validated, err := ValidateArtifact(target)
	if err != nil || validated.SealSHA256 != report.SealSHA256 {
		return Receipt{}, ErrInvalidArtifact
	}
	return Receipt{Created: true, Report: validated}, nil
}

func (service *Service) snapshot(ctx context.Context, reviewer string) (catalogreview.Snapshot, catalogreview.Progress, string, error) {
	if service == nil || service.reviews == nil || strings.TrimSpace(service.catalogPath) == "" || strings.TrimSpace(service.reviewManifestPath) == "" || strings.TrimSpace(service.outputRoot) == "" || strings.TrimSpace(reviewer) == "" {
		return catalogreview.Snapshot{}, catalogreview.Progress{}, "", ErrUnavailable
	}
	snapshot, progress, err := service.reviews.SealSnapshot(ctx, reviewer)
	if err != nil {
		return catalogreview.Snapshot{}, catalogreview.Progress{}, "", err
	}
	if len(snapshot.CatalogSHA256) != 64 || len(progress.ReviewSetSHA256) != 64 || progress.Total != len(snapshot.Cases) {
		return catalogreview.Snapshot{}, catalogreview.Progress{}, "", ErrInvalidArtifact
	}
	return snapshot, progress, digest(strings.TrimSpace(reviewer)), nil
}

func (service *Service) materialize(root, id, reviewerHash string, snapshot catalogreview.Snapshot, progress catalogreview.Progress) (Report, error) {
	originalCatalogBytes, err := os.ReadFile(service.catalogPath)
	if err != nil {
		return Report{}, ErrUnavailable
	}
	originalCatalog, _, err := evaluation.LoadEvalCatalogManifest(bytes.NewReader(originalCatalogBytes))
	if err != nil || digestBytes(originalCatalogBytes) != snapshot.CatalogSHA256 || originalCatalog.TotalCases != 320 {
		return Report{}, ErrInvalidArtifact
	}
	originalReviewBytes, err := os.ReadFile(service.reviewManifestPath)
	if err != nil {
		return Report{}, ErrUnavailable
	}
	var originalReview evaluation.ReviewManifest
	if err := decodeStrict(originalReviewBytes, &originalReview, 1<<20); err != nil {
		return Report{}, ErrInvalidArtifact
	}
	validatedReview, err := evaluation.ValidateReviewManifestFile(service.reviewManifestPath, service.catalogPath)
	if err != nil || !validatedReview.Passed || validatedReview.CatalogManifestSHA256 != snapshot.CatalogSHA256 {
		return Report{}, ErrInvalidArtifact
	}
	evalRoot := filepath.Join(root, "evals")
	if err := os.MkdirAll(evalRoot, 0o750); err != nil {
		return Report{}, ErrUnavailable
	}
	casesBySlice := make(map[string][]catalogreview.Case, len(originalCatalog.Slices))
	for _, item := range snapshot.Cases {
		casesBySlice[item.Slice] = append(casesBySlice[item.Slice], item)
	}
	sealedCatalog := originalCatalog
	sealedCatalog.Slices = append([]evaluation.EvalCatalogSliceManifest(nil), originalCatalog.Slices...)
	reviewBySlice := make(map[string]evaluation.ReviewManifestSlice, len(originalReview.Slices))
	for _, item := range originalReview.Slices {
		reviewBySlice[item.Name] = item
	}
	createdAt := service.clock().UTC()
	sealedReview := originalReview
	sealedReview.ManifestVersion = "devsupport-eval-review-sealed-" + id[len("catalog-seal-"):len("catalog-seal-")+12]
	sealedReview.CreatedAt = createdAt
	sealedReview.Slices = make([]evaluation.ReviewManifestSlice, 0, len(originalCatalog.Slices))
	files := make([]FileEntry, 0, len(originalCatalog.Slices)+len(originalReview.Fixtures)+2)
	for index, definition := range sealedCatalog.Slices {
		cases := casesBySlice[definition.Name]
		if len(cases) != definition.ExpectedCount {
			return Report{}, ErrInvalidArtifact
		}
		path, err := safeJoin(evalRoot, definition.Path)
		if err != nil {
			return Report{}, ErrInvalidArtifact
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return Report{}, ErrUnavailable
		}
		var buffer bytes.Buffer
		for _, item := range cases {
			row := make(map[string]any, len(item.Content)+3)
			for key, value := range item.Content {
				row[key] = value
			}
			row["id"], row["dataset_version"], row["reviewed_by"] = item.ID, item.DatasetVersion, "human"
			encoded, marshalErr := json.Marshal(row)
			if marshalErr != nil {
				return Report{}, ErrInvalidArtifact
			}
			buffer.Write(encoded)
			buffer.WriteByte('\n')
		}
		if err := writeExclusive(path, buffer.Bytes(), 0o640); err != nil {
			return Report{}, err
		}
		entry, err := fileEntry(root, path)
		if err != nil {
			return Report{}, err
		}
		files = append(files, entry)
		sealedCatalog.Slices[index].SHA256 = entry.SHA256
		sealedCatalog.Slices[index].ReviewPolicy = "human_only"
		sourceReview, exists := reviewBySlice[definition.Name]
		if !exists {
			return Report{}, ErrInvalidArtifact
		}
		sourceReview.ArtifactSHA256, sourceReview.ReviewStatus = entry.SHA256, "reviewed"
		sourceReview.Reviewer, sourceReview.ReviewedAt = "sha256:"+reviewerHash, &createdAt
		sealedReview.Slices = append(sealedReview.Slices, sourceReview)
	}
	sealedReview.Fixtures = append([]evaluation.ReviewFixture(nil), originalReview.Fixtures...)
	for _, fixture := range sealedReview.Fixtures {
		sourcePath, err := safeJoin(filepath.Dir(service.reviewManifestPath), fixture.Path)
		if err != nil {
			return Report{}, ErrInvalidArtifact
		}
		destination, err := safeJoin(evalRoot, fixture.Path)
		if err != nil {
			return Report{}, ErrInvalidArtifact
		}
		encoded, err := os.ReadFile(sourcePath)
		if err != nil || digestBytes(encoded) != strings.ToLower(fixture.SHA256) {
			return Report{}, ErrInvalidArtifact
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
			return Report{}, ErrUnavailable
		}
		if err := writeExclusive(destination, encoded, 0o640); err != nil {
			return Report{}, err
		}
		entry, err := fileEntry(root, destination)
		if err != nil {
			return Report{}, err
		}
		files = append(files, entry)
	}
	catalogBytes, err := json.MarshalIndent(sealedCatalog, "", "  ")
	if err != nil {
		return Report{}, ErrInvalidArtifact
	}
	catalogBytes = append(catalogBytes, '\n')
	catalogPath := filepath.Join(evalRoot, sealedCatalogName)
	if err := writeExclusive(catalogPath, catalogBytes, 0o640); err != nil {
		return Report{}, err
	}
	sealedReview.CatalogManifestSHA256 = digestBytes(catalogBytes)
	reviewBytes, err := json.MarshalIndent(sealedReview, "", "  ")
	if err != nil {
		return Report{}, ErrInvalidArtifact
	}
	reviewBytes = append(reviewBytes, '\n')
	reviewPath := filepath.Join(evalRoot, sealedReviewName)
	if err := writeExclusive(reviewPath, reviewBytes, 0o640); err != nil {
		return Report{}, err
	}
	for _, path := range []string{catalogPath, reviewPath} {
		entry, entryErr := fileEntry(root, path)
		if entryErr != nil {
			return Report{}, entryErr
		}
		files = append(files, entry)
	}
	catalogValidation, err := evaluation.ValidateEvalCatalogFile(catalogPath)
	if err != nil || !catalogValidation.Passed || catalogValidation.ActualTotal != 320 {
		return Report{}, ErrInvalidArtifact
	}
	reviewValidation, err := evaluation.ValidateReviewManifestFile(reviewPath, catalogPath)
	if err != nil || !reviewValidation.Passed || !reviewValidation.HumanReviewed || !reviewValidation.BaselineEligible || reviewValidation.ReviewedCases != 320 {
		return Report{}, ErrInvalidArtifact
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	report := Report{
		SchemaVersion: SchemaVersion, SealID: id, Status: "sealed_candidate_ready", CreatedAt: createdAt,
		DatasetVersion: snapshot.DatasetVersion, SourceCatalogSHA256: snapshot.CatalogSHA256,
		SourceReviewManifestSHA256: digestBytes(originalReviewBytes), SourceReviewSetSHA256: progress.ReviewSetSHA256,
		ReviewerHash: reviewerHash, CaseCount: progress.Total, ApprovedCases: progress.Approved,
		OutputCatalogSHA256: digestBytes(catalogBytes), OutputReviewSHA256: digestBytes(reviewBytes), Files: files,
		NextRequiredGate: "从本候选目录重新运行 Intent/RAG/Diagnosis/Tool/Memory 五类评测与统一 Runner；技术门和 Judge 校准通过后才可提交基线冻结审批。",
		Guardrails:       []string{"source_database_review_set_bound", "reviewer_identity_hashed", "all_cases_materialized_as_human", "catalog_and_fixture_hash_verified", "immutable_directory", "self_hash_is_not_digital_signature", "no_baseline_pointer_write", "no_policy_write"},
	}
	if err := finalizeReport(&report); err != nil {
		return Report{}, err
	}
	reportBytes, _ := json.MarshalIndent(report, "", "  ")
	if err := writeExclusive(filepath.Join(root, "seal.json"), append(reportBytes, '\n'), 0o640); err != nil {
		return Report{}, err
	}
	return report, nil
}

func ValidateArtifact(root string) (Report, error) {
	var report Report
	encoded, err := os.ReadFile(filepath.Join(root, "seal.json"))
	if err != nil {
		return report, err
	}
	if err := decodeStrict(encoded, &report, 1<<20); err != nil || validateReport(report) != nil {
		return Report{}, ErrInvalidArtifact
	}
	expected := report.SealSHA256
	copyReport := report
	if err := finalizeReport(&copyReport); err != nil || copyReport.SealSHA256 != expected {
		return Report{}, ErrInvalidArtifact
	}
	expectedFiles := map[string]struct{}{"seal.json": {}}
	for _, entry := range report.Files {
		expectedFiles[entry.Path] = struct{}{}
		path, err := safeJoin(root, entry.Path)
		if err != nil {
			return Report{}, ErrInvalidArtifact
		}
		info, err := os.Stat(path)
		content, readErr := os.ReadFile(path)
		if err != nil || readErr != nil || !info.Mode().IsRegular() || info.Size() != entry.Bytes || digestBytes(content) != entry.SHA256 {
			return Report{}, ErrInvalidArtifact
		}
	}
	actualFiles := 0
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return ErrInvalidArtifact
		}
		if path == root || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return ErrInvalidArtifact
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return ErrInvalidArtifact
		}
		relative = filepath.ToSlash(relative)
		if _, exists := expectedFiles[relative]; !exists {
			return ErrInvalidArtifact
		}
		actualFiles++
		return nil
	}); err != nil || actualFiles != len(expectedFiles) {
		return Report{}, ErrInvalidArtifact
	}
	catalogPath := filepath.Join(root, "evals", sealedCatalogName)
	reviewPath := filepath.Join(root, "evals", sealedReviewName)
	catalog, catalogErr := evaluation.ValidateEvalCatalogFile(catalogPath)
	review, reviewErr := evaluation.ValidateReviewManifestFile(reviewPath, catalogPath)
	if catalogErr != nil || reviewErr != nil || !catalog.Passed || !review.Passed || !review.BaselineEligible || catalog.ManifestSHA256 != report.OutputCatalogSHA256 || review.ManifestSHA256 != report.OutputReviewSHA256 {
		return Report{}, ErrInvalidArtifact
	}
	return report, nil
}

func validateReport(report Report) error {
	if report.SchemaVersion != SchemaVersion || !strings.HasPrefix(report.SealID, "catalog-seal-") || len(report.SealID) != len("catalog-seal-")+32 || report.Status != "sealed_candidate_ready" || report.CreatedAt.IsZero() || strings.TrimSpace(report.DatasetVersion) == "" || len(report.SourceCatalogSHA256) != 64 || len(report.SourceReviewManifestSHA256) != 64 || len(report.SourceReviewSetSHA256) != 64 || len(report.ReviewerHash) != 64 || report.CaseCount != 320 || report.ApprovedCases != 320 || len(report.OutputCatalogSHA256) != 64 || len(report.OutputReviewSHA256) != 64 || len(report.Files) != 11 || len(report.Guardrails) == 0 || strings.TrimSpace(report.NextRequiredGate) == "" || len(report.SealSHA256) != 64 {
		return ErrInvalidArtifact
	}
	seen := map[string]struct{}{}
	for index, entry := range report.Files {
		if strings.TrimSpace(entry.Path) == "" || filepath.IsAbs(entry.Path) || len(entry.SHA256) != 64 || entry.Bytes <= 0 || (index > 0 && report.Files[index-1].Path >= entry.Path) {
			return ErrInvalidArtifact
		}
		if _, duplicate := seen[entry.Path]; duplicate {
			return ErrInvalidArtifact
		}
		seen[entry.Path] = struct{}{}
	}
	return nil
}

func finalizeReport(report *Report) error {
	if report == nil {
		return ErrInvalidArtifact
	}
	report.SealSHA256 = strings.Repeat("0", 64)
	if err := validateReport(*report); err != nil {
		return err
	}
	report.SealSHA256 = ""
	encoded, err := json.Marshal(report)
	if err != nil {
		return err
	}
	report.SealSHA256 = digestBytes(encoded)
	return nil
}

func sealID(catalogSHA, reviewSetSHA, reviewerHash string) string {
	return "catalog-seal-" + digest(strings.Join([]string{SchemaVersion, catalogSHA, reviewSetSHA, reviewerHash}, "\x00"))[:32]
}

func fileEntry(root, path string) (FileEntry, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return FileEntry{}, ErrUnavailable
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return FileEntry{}, ErrInvalidArtifact
	}
	return FileEntry{Path: filepath.ToSlash(relative), SHA256: digestBytes(content), Bytes: int64(len(content))}, nil
}

func safeJoin(root, relative string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil || filepath.IsAbs(relative) {
		return "", ErrInvalidArtifact
	}
	candidate, err := filepath.Abs(filepath.Join(root, filepath.Clean(relative)))
	if err != nil {
		return "", ErrInvalidArtifact
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrInvalidArtifact
	}
	return candidate, nil
}

func writeExclusive(path string, content []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return ErrUnavailable
	}
	var written int
	if written, err = file.Write(content); err == nil && written != len(content) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return ErrUnavailable
	}
	return nil
}

func decodeStrict(encoded []byte, target any, maximum int64) error {
	if len(encoded) == 0 || int64(len(encoded)) > maximum {
		return ErrInvalidArtifact
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidArtifact
	}
	return nil
}

func digest(value string) string { return digestBytes([]byte(value)) }

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
