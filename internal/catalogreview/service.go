package catalogreview

import (
	"bufio"
	"bytes"
	"context"
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
	"time"

	evaldomain "GopherAI/internal/evaluation"
	"GopherAI/model"
)

const (
	SchemaVersion       = "evaluation-catalog-review-workbench-v1"
	ReviewSchemaVersion = "evaluation-catalog-human-review-v1"
	DefaultManifestPath = "evals/devsupport-eval-v1.manifest.json"
	Acknowledgment      = "I_REVIEWED_CASE_AND_EXPECTED_RESULT"
)

var (
	ErrArtifactUnavailable = errors.New("evaluation catalog review artifact is unavailable")
	ErrInvalidQuery        = errors.New("evaluation catalog review query is invalid")
	ErrInvalidReview       = errors.New("evaluation catalog review is invalid")
	ErrCaseNotFound        = errors.New("evaluation catalog review case was not found")
	ErrRevisionConflict    = errors.New("evaluation catalog review revision conflict")
	ErrIdempotencyConflict = errors.New("evaluation catalog review idempotency conflict")
	idempotencyPattern     = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)
	allowedRejectReasons   = map[string]struct{}{
		"ambiguous_input": {}, "expected_result_incorrect": {}, "missing_context": {},
		"schema_issue": {}, "unsafe_or_sensitive": {},
	}
)

type Case struct {
	ID             string
	Slice          string
	DatasetVersion string
	CaseSHA256     string
	Prompt         string
	Content        map[string]any
}

type Snapshot struct {
	DatasetVersion string
	CatalogSHA256  string
	Cases          []Case
}

type ArtifactStore interface{ Load() (Snapshot, error) }

type FileArtifactStore struct{ manifestPath string }

func NewFileArtifactStore(manifestPath string) *FileArtifactStore {
	return &FileArtifactStore{manifestPath: manifestPath}
}

func (store *FileArtifactStore) Load() (Snapshot, error) {
	if store == nil || strings.TrimSpace(store.manifestPath) == "" {
		return Snapshot{}, ErrArtifactUnavailable
	}
	report, err := evaldomain.ValidateEvalCatalogFile(store.manifestPath)
	if err != nil || !report.Passed {
		return Snapshot{}, ErrArtifactUnavailable
	}
	manifestBytes, err := os.ReadFile(store.manifestPath)
	if err != nil {
		return Snapshot{}, ErrArtifactUnavailable
	}
	manifest, _, err := evaldomain.LoadEvalCatalogManifest(bytes.NewReader(manifestBytes))
	if err != nil {
		return Snapshot{}, ErrArtifactUnavailable
	}
	base, err := filepath.Abs(filepath.Dir(store.manifestPath))
	if err != nil {
		return Snapshot{}, ErrArtifactUnavailable
	}
	snapshot := Snapshot{DatasetVersion: manifest.DatasetVersion, CatalogSHA256: report.ManifestSHA256, Cases: make([]Case, 0, manifest.TotalCases)}
	for _, slice := range manifest.Slices {
		path, pathErr := safePath(base, slice.Path)
		if pathErr != nil {
			return Snapshot{}, ErrArtifactUnavailable
		}
		cases, loadErr := loadCases(path, slice.Name)
		if loadErr != nil || len(cases) != slice.ExpectedCount {
			return Snapshot{}, ErrArtifactUnavailable
		}
		snapshot.Cases = append(snapshot.Cases, cases...)
	}
	if len(snapshot.Cases) != manifest.TotalCases {
		return Snapshot{}, ErrArtifactUnavailable
	}
	return snapshot, nil
}

func safePath(base, relative string) (string, error) {
	candidate, err := filepath.Abs(filepath.Join(base, filepath.Clean(relative)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("catalog path escapes base directory")
	}
	return candidate, nil
}

func loadCases(path, slice string) ([]Case, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	result := make([]Case, 0, 160)
	for scanner.Scan() {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		content := map[string]any{}
		if err := decoder.Decode(&content); err != nil {
			return nil, err
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return nil, errors.New("catalog case has trailing content")
		}
		id, _ := content["id"].(string)
		datasetVersion, _ := content["dataset_version"].(string)
		reviewedBy, _ := content["reviewed_by"].(string)
		id, datasetVersion, reviewedBy = strings.TrimSpace(id), strings.TrimSpace(datasetVersion), strings.TrimSpace(reviewedBy)
		if id == "" || datasetVersion == "" || (reviewedBy != "pending_user" && reviewedBy != "human") {
			return nil, errors.New("catalog case identity is invalid")
		}
		digest := sha256.Sum256(raw)
		delete(content, "id")
		delete(content, "dataset_version")
		delete(content, "reviewed_by")
		result = append(result, Case{ID: id, Slice: slice, DatasetVersion: datasetVersion, CaseSHA256: hex.EncodeToString(digest[:]), Prompt: casePrompt(content), Content: content})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func casePrompt(content map[string]any) string {
	for _, key := range []string{"question", "message", "query", "scenario"} {
		if value, ok := content[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "该用例没有单一问题字段，请展开结构化内容复核。"
}

type Query struct {
	Slice    string
	Status   string
	Page     int
	PageSize int
}

type ReviewView struct {
	Decision     string    `json:"decision"`
	ReasonCodes  []string  `json:"reason_codes"`
	Revision     int       `json:"revision"`
	ReviewSHA256 string    `json:"review_sha256"`
	ReviewedAt   time.Time `json:"reviewed_at"`
}

type CaseView struct {
	ID             string         `json:"id"`
	Slice          string         `json:"slice"`
	DatasetVersion string         `json:"dataset_version"`
	CaseSHA256     string         `json:"case_sha256"`
	Prompt         string         `json:"prompt"`
	Content        map[string]any `json:"content"`
	Review         *ReviewView    `json:"review,omitempty"`
}

type SliceProgress struct {
	Total    int `json:"total"`
	Reviewed int `json:"reviewed"`
	Approved int `json:"approved"`
	Rejected int `json:"rejected"`
	Pending  int `json:"pending"`
}

type Progress struct {
	Total                 int                      `json:"total"`
	Reviewed              int                      `json:"reviewed"`
	Approved              int                      `json:"approved"`
	Rejected              int                      `json:"rejected"`
	Pending               int                      `json:"pending"`
	BySlice               map[string]SliceProgress `json:"by_slice"`
	ReviewSetSHA256       string                   `json:"review_set_sha256"`
	ReadyForMaterializing bool                     `json:"ready_for_sealed_materialization"`
}

type Workbench struct {
	SchemaVersion  string     `json:"schema_version"`
	DatasetVersion string     `json:"dataset_version"`
	CatalogSHA256  string     `json:"catalog_sha256"`
	Status         string     `json:"status"`
	Progress       Progress   `json:"progress"`
	SliceFilter    string     `json:"slice_filter"`
	StatusFilter   string     `json:"status_filter"`
	Page           int        `json:"page"`
	PageSize       int        `json:"page_size"`
	FilteredTotal  int        `json:"filtered_total"`
	Cases          []CaseView `json:"cases"`
	Guardrails     []string   `json:"guardrails"`
	Limitations    []string   `json:"limitations"`
}

type ReviewCommand struct {
	CatalogSHA256    string
	CaseID           string
	CaseSHA256       string
	ExpectedRevision int
	Decision         string
	ReasonCodes      []string
	IdempotencyKey   string
	Acknowledgment   string
}

type Receipt struct {
	SchemaVersion string     `json:"schema_version"`
	Created       bool       `json:"created"`
	CaseID        string     `json:"case_id"`
	Review        ReviewView `json:"review"`
	Progress      Progress   `json:"progress"`
}

type Service struct {
	artifacts  ArtifactStore
	repository Repository
	clock      func() time.Time
}

func NewService(artifacts ArtifactStore, repository Repository, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{artifacts: artifacts, repository: repository, clock: clock}
}

func (service *Service) List(ctx context.Context, reviewer string, query Query) (Workbench, error) {
	if service == nil || service.artifacts == nil || service.repository == nil || strings.TrimSpace(reviewer) == "" {
		return Workbench{}, ErrArtifactUnavailable
	}
	if query.Page == 0 {
		query.Page = 1
	}
	if query.PageSize == 0 {
		query.PageSize = 1
	}
	if query.Page < 1 || query.PageSize < 1 || query.PageSize > 20 {
		return Workbench{}, ErrInvalidQuery
	}
	query.Slice, query.Status = strings.TrimSpace(query.Slice), strings.TrimSpace(query.Status)
	if query.Status == "" {
		query.Status = "pending"
	}
	if query.Status != "all" && query.Status != "pending" && query.Status != "reviewed" && query.Status != "approved" && query.Status != "rejected" {
		return Workbench{}, ErrInvalidQuery
	}
	snapshot, reviews, reviewByCase, err := service.load(ctx, reviewer)
	if err != nil {
		return Workbench{}, err
	}
	if query.Slice != "" {
		found := false
		for _, item := range snapshot.Cases {
			if item.Slice == query.Slice {
				found = true
				break
			}
		}
		if !found {
			return Workbench{}, ErrInvalidQuery
		}
	}
	filtered := make([]CaseView, 0, len(snapshot.Cases))
	for _, item := range snapshot.Cases {
		if query.Slice != "" && item.Slice != query.Slice {
			continue
		}
		review, reviewed := reviewByCase[item.ID]
		if !matchesStatus(query.Status, reviewed, review.Decision) {
			continue
		}
		view := CaseView{ID: item.ID, Slice: item.Slice, DatasetVersion: item.DatasetVersion, CaseSHA256: item.CaseSHA256, Prompt: item.Prompt, Content: item.Content}
		if reviewed {
			value, viewErr := reviewView(review)
			if viewErr != nil {
				return Workbench{}, viewErr
			}
			view.Review = &value
		}
		filtered = append(filtered, view)
	}
	start := (query.Page - 1) * query.PageSize
	pageCases := []CaseView{}
	if start < len(filtered) {
		end := start + query.PageSize
		if end > len(filtered) {
			end = len(filtered)
		}
		pageCases = filtered[start:end]
	}
	progress, err := buildProgress(snapshot, reviews)
	if err != nil {
		return Workbench{}, err
	}
	status := "human_review_in_progress"
	if progress.Rejected > 0 {
		status = "correction_required"
	}
	if progress.ReadyForMaterializing {
		status = "ready_for_sealed_materialization"
	}
	return Workbench{
		SchemaVersion: SchemaVersion, DatasetVersion: snapshot.DatasetVersion, CatalogSHA256: snapshot.CatalogSHA256,
		Status: status, Progress: progress, SliceFilter: query.Slice, StatusFilter: query.Status,
		Page: query.Page, PageSize: query.PageSize, FilteredTotal: len(filtered), Cases: pageCases,
		Guardrails:  []string{"authenticated_reviewer_scope", "catalog_and_case_hash_bound", "append_only_revisions", "optimistic_revision_check", "idempotent_submission", "no_dataset_mutation", "no_baseline_auto_freeze"},
		Limitations: []string{"复核进度只属于当前登录用户，不代表双人独立标注。", "320 条全部通过只形成可封存候选；冻结数据集、重跑评测和基线晋级必须走后续独立门禁。"},
	}, nil
}

func (service *Service) Submit(ctx context.Context, reviewer string, command ReviewCommand) (Receipt, error) {
	reviewer = strings.TrimSpace(reviewer)
	command.CatalogSHA256, command.CaseID, command.CaseSHA256 = strings.TrimSpace(command.CatalogSHA256), strings.TrimSpace(command.CaseID), strings.TrimSpace(command.CaseSHA256)
	command.Decision, command.IdempotencyKey = strings.TrimSpace(command.Decision), strings.TrimSpace(command.IdempotencyKey)
	if service == nil || service.artifacts == nil || service.repository == nil || reviewer == "" || command.ExpectedRevision < 0 || command.Acknowledgment != Acknowledgment || !idempotencyPattern.MatchString(command.IdempotencyKey) {
		return Receipt{}, ErrInvalidReview
	}
	reasons, err := normalizeReasons(command.Decision, command.ReasonCodes)
	if err != nil {
		return Receipt{}, err
	}
	snapshot, _, _, err := service.load(ctx, reviewer)
	if err != nil {
		return Receipt{}, err
	}
	if command.CatalogSHA256 != snapshot.CatalogSHA256 {
		return Receipt{}, ErrRevisionConflict
	}
	var selected *Case
	for index := range snapshot.Cases {
		if snapshot.Cases[index].ID == command.CaseID {
			selected = &snapshot.Cases[index]
			break
		}
	}
	if selected == nil {
		return Receipt{}, ErrCaseNotFound
	}
	if selected.CaseSHA256 != command.CaseSHA256 {
		return Receipt{}, ErrRevisionConflict
	}
	reviewerHash := digest(reviewer)
	reasonJSON, _ := json.Marshal(reasons)
	requestSHA := digest(strings.Join([]string{snapshot.CatalogSHA256, selected.ID, selected.CaseSHA256, reviewerHash, fmt.Sprint(command.ExpectedRevision), command.Decision, string(reasonJSON)}, "\x00"))
	idempotencyHash := digest(reviewerHash + "\x00" + command.IdempotencyKey)
	now := service.clock().UTC()
	revision := command.ExpectedRevision + 1
	reviewSHA := digest(strings.Join([]string{ReviewSchemaVersion, snapshot.DatasetVersion, snapshot.CatalogSHA256, selected.Slice, selected.ID, selected.CaseSHA256, reviewerHash, fmt.Sprint(revision), command.Decision, string(reasonJSON), fmt.Sprint(command.ExpectedRevision), idempotencyHash, requestSHA, now.Format(time.RFC3339Nano)}, "\x00"))
	candidate := model.EvaluationCatalogReview{
		ID: reviewSHA, SchemaVersion: ReviewSchemaVersion, DatasetVersion: snapshot.DatasetVersion, CatalogSHA256: snapshot.CatalogSHA256,
		Slice: selected.Slice, CaseID: selected.ID, CaseSHA256: selected.CaseSHA256, ReviewerHash: reviewerHash,
		Revision: revision, Decision: command.Decision, ReasonCodesJSON: string(reasonJSON), ExpectedRevision: command.ExpectedRevision,
		IdempotencyKeyHash: idempotencyHash, RequestSHA256: requestSHA, ReviewSHA256: reviewSHA, CreatedAt: now,
	}
	created, stored, err := service.repository.Append(ctx, candidate)
	if err != nil {
		return Receipt{}, err
	}
	view, err := reviewView(stored)
	if err != nil {
		return Receipt{}, err
	}
	latestReviews, err := service.repository.ListLatest(ctx, snapshot.CatalogSHA256, reviewerHash)
	if err != nil {
		return Receipt{}, err
	}
	progress, err := buildProgress(snapshot, latestReviews)
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{SchemaVersion: ReviewSchemaVersion, Created: created, CaseID: stored.CaseID, Review: view, Progress: progress}, nil
}

func (service *Service) load(ctx context.Context, reviewer string) (Snapshot, []model.EvaluationCatalogReview, map[string]model.EvaluationCatalogReview, error) {
	snapshot, err := service.artifacts.Load()
	if err != nil {
		return Snapshot{}, nil, nil, err
	}
	reviews, err := service.repository.ListLatest(ctx, snapshot.CatalogSHA256, digest(reviewer))
	if err != nil {
		return Snapshot{}, nil, nil, err
	}
	byCase := make(map[string]model.EvaluationCatalogReview, len(reviews))
	for _, review := range reviews {
		if err := validateReview(review); err != nil || review.CatalogSHA256 != snapshot.CatalogSHA256 {
			return Snapshot{}, nil, nil, ErrArtifactUnavailable
		}
		byCase[review.CaseID] = review
	}
	return snapshot, reviews, byCase, nil
}

func normalizeReasons(decision string, values []string) ([]string, error) {
	if decision != "approved" && decision != "rejected" {
		return nil, ErrInvalidReview
	}
	seen := map[string]struct{}{}
	reasons := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, ErrInvalidReview
		}
		seen[value] = struct{}{}
		reasons = append(reasons, value)
	}
	sort.Strings(reasons)
	if decision == "approved" {
		if len(reasons) != 1 || reasons[0] != "label_verified" {
			return nil, ErrInvalidReview
		}
		return reasons, nil
	}
	if len(reasons) < 1 || len(reasons) > 3 {
		return nil, ErrInvalidReview
	}
	for _, reason := range reasons {
		if _, allowed := allowedRejectReasons[reason]; !allowed {
			return nil, ErrInvalidReview
		}
	}
	return reasons, nil
}

func matchesStatus(filter string, reviewed bool, decision string) bool {
	switch filter {
	case "all":
		return true
	case "pending":
		return !reviewed
	case "reviewed":
		return reviewed
	case "approved", "rejected":
		return reviewed && decision == filter
	default:
		return false
	}
}

func reviewView(review model.EvaluationCatalogReview) (ReviewView, error) {
	if err := validateReview(review); err != nil {
		return ReviewView{}, err
	}
	reasons := []string{}
	if err := json.Unmarshal([]byte(review.ReasonCodesJSON), &reasons); err != nil {
		return ReviewView{}, ErrArtifactUnavailable
	}
	return ReviewView{Decision: review.Decision, ReasonCodes: reasons, Revision: review.Revision, ReviewSHA256: review.ReviewSHA256, ReviewedAt: review.CreatedAt.UTC()}, nil
}

func validateReview(review model.EvaluationCatalogReview) error {
	if review.SchemaVersion != ReviewSchemaVersion || review.ID != review.ReviewSHA256 || len(review.ID) != 64 || strings.TrimSpace(review.DatasetVersion) == "" || len(review.CatalogSHA256) != 64 || strings.TrimSpace(review.Slice) == "" || strings.TrimSpace(review.CaseID) == "" || len(review.CaseSHA256) != 64 || len(review.ReviewerHash) != 64 || review.Revision < 1 || review.ExpectedRevision != review.Revision-1 || len(review.IdempotencyKeyHash) != 64 || len(review.RequestSHA256) != 64 || review.CreatedAt.IsZero() {
		return ErrArtifactUnavailable
	}
	reasons := []string{}
	if json.Unmarshal([]byte(review.ReasonCodesJSON), &reasons) != nil {
		return ErrArtifactUnavailable
	}
	if _, err := normalizeReasons(review.Decision, reasons); err != nil {
		return ErrArtifactUnavailable
	}
	expected := digest(strings.Join([]string{review.SchemaVersion, review.DatasetVersion, review.CatalogSHA256, review.Slice, review.CaseID, review.CaseSHA256, review.ReviewerHash, fmt.Sprint(review.Revision), review.Decision, review.ReasonCodesJSON, fmt.Sprint(review.ExpectedRevision), review.IdempotencyKeyHash, review.RequestSHA256, review.CreatedAt.UTC().Format(time.RFC3339Nano)}, "\x00"))
	if review.ReviewSHA256 != expected {
		return ErrArtifactUnavailable
	}
	return nil
}

func buildProgress(snapshot Snapshot, reviews []model.EvaluationCatalogReview) (Progress, error) {
	progress := Progress{Total: len(snapshot.Cases), BySlice: map[string]SliceProgress{}}
	caseSlice := make(map[string]string, len(snapshot.Cases))
	for _, item := range snapshot.Cases {
		caseSlice[item.ID] = item.Slice
		value := progress.BySlice[item.Slice]
		value.Total++
		progress.BySlice[item.Slice] = value
	}
	seen, hashes := map[string]struct{}{}, make([]string, 0, len(reviews))
	for _, review := range reviews {
		if err := validateReview(review); err != nil || review.CatalogSHA256 != snapshot.CatalogSHA256 || caseSlice[review.CaseID] != review.Slice {
			return Progress{}, ErrArtifactUnavailable
		}
		if _, duplicate := seen[review.CaseID]; duplicate {
			return Progress{}, ErrArtifactUnavailable
		}
		seen[review.CaseID] = struct{}{}
		progress.Reviewed++
		value := progress.BySlice[review.Slice]
		value.Reviewed++
		if review.Decision == "approved" {
			progress.Approved++
			value.Approved++
		} else {
			progress.Rejected++
			value.Rejected++
		}
		progress.BySlice[review.Slice] = value
		hashes = append(hashes, review.CaseID+"\x00"+review.ReviewSHA256)
	}
	progress.Pending = progress.Total - progress.Reviewed
	for key, value := range progress.BySlice {
		value.Pending = value.Total - value.Reviewed
		progress.BySlice[key] = value
	}
	sort.Strings(hashes)
	progress.ReviewSetSHA256 = digest(strings.Join(hashes, "\x00"))
	progress.ReadyForMaterializing = progress.Total == 320 && progress.Reviewed == progress.Total && progress.Approved == progress.Total && progress.Rejected == 0
	return progress, nil
}

func digest(value string) string {
	result := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(result[:])
}
