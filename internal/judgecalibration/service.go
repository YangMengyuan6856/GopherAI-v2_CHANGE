package judgecalibration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"GopherAI/internal/contract"
	"GopherAI/internal/evalgovernance"
	"GopherAI/internal/evaluation"
	"GopherAI/model"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	SchemaVersion             = "judge-calibration-audit-v1"
	LegacyReviewSchemaVersion = "judge-calibration-human-review-v1"
	ReviewSchemaVersion       = "judge-calibration-human-review-v2"
	DefaultDatasetPath        = "evals/devsupport-judge-calibration-v1.jsonl"
	DefaultReportPath         = "/root/GopherAI_Runtime/evaluation/judge-calibration-latest.json"
	DefaultGovernancePath     = "evals/devsupport-eval-v1.governance.json"
	DefaultCatalogPath        = "evals/devsupport-eval-v1.manifest.json"
)

var (
	ErrArtifactUnavailable = errors.New("judge calibration artifact is unavailable")
	ErrInvalidReview       = errors.New("judge calibration review is invalid")
	ErrCaseNotFound        = errors.New("judge calibration case was not found")
)

type ArtifactStore interface {
	Load() ([]evaluation.JudgeCalibrationCase, evaluation.JudgeCalibrationReport, error)
}

type FileArtifactStore struct{ datasetPath, reportPath string }

func NewFileArtifactStore(datasetPath, reportPath string) *FileArtifactStore {
	return &FileArtifactStore{datasetPath: datasetPath, reportPath: reportPath}
}

func (store *FileArtifactStore) Load() ([]evaluation.JudgeCalibrationCase, evaluation.JudgeCalibrationReport, error) {
	if store == nil || strings.TrimSpace(store.datasetPath) == "" || strings.TrimSpace(store.reportPath) == "" {
		return nil, evaluation.JudgeCalibrationReport{}, ErrArtifactUnavailable
	}
	content, err := os.ReadFile(store.datasetPath)
	if err != nil {
		return nil, evaluation.JudgeCalibrationReport{}, err
	}
	cases, err := evaluation.LoadJudgeCalibrationCases(bytes.NewReader(content))
	if err != nil {
		return nil, evaluation.JudgeCalibrationReport{}, err
	}
	report, err := evaluation.LoadJudgeCalibrationReport(store.reportPath)
	if err != nil {
		return nil, evaluation.JudgeCalibrationReport{}, err
	}
	datasetDigest := sha256.Sum256(content)
	if report.DatasetSHA256 != hex.EncodeToString(datasetDigest[:]) {
		return nil, evaluation.JudgeCalibrationReport{}, errors.New("judge calibration report does not match dataset hash")
	}
	resultByID := make(map[string]evaluation.JudgeCalibrationCaseResult, len(report.Cases))
	for _, result := range report.Cases {
		resultByID[result.ID] = result
	}
	for _, item := range cases {
		encoded, _ := json.Marshal(item)
		digest := sha256.Sum256(encoded)
		result, exists := resultByID[item.ID]
		if !exists || result.CaseSHA256 != hex.EncodeToString(digest[:]) {
			return nil, evaluation.JudgeCalibrationReport{}, errors.New("judge calibration case hash mismatch")
		}
	}
	return cases, report, nil
}

type EvidenceView struct {
	ID       string `json:"id"`
	SourceID string `json:"source_id"`
	Origin   string `json:"origin"`
	Content  string `json:"content"`
}

type CaseView struct {
	ID              string                                `json:"id"`
	Slice           string                                `json:"slice"`
	TaskType        string                                `json:"task_type"`
	Question        string                                `json:"question"`
	Answer          string                                `json:"answer"`
	Evidence        []EvidenceView                        `json:"evidence"`
	ExpectedFacts   []string                              `json:"expected_facts"`
	ForbiddenClaims []string                              `json:"forbidden_claims"`
	AnswerVariant   string                                `json:"answer_variant,omitempty"`
	Judge           evaluation.JudgeCalibrationCaseResult `json:"judge"`
	HumanScores     *evaluation.JudgeScores               `json:"human_scores,omitempty"`
	HumanComment    string                                `json:"human_comment,omitempty"`
	ReviewRevision  int                                   `json:"review_revision"`
}

type Audit struct {
	SchemaVersion     string                              `json:"schema_version"`
	DatasetVersion    string                              `json:"dataset_version"`
	DatasetSHA256     string                              `json:"dataset_sha256"`
	ReportSHA256      string                              `json:"report_sha256"`
	JudgeModel        string                              `json:"judge_model"`
	JudgePrompt       string                              `json:"judge_prompt"`
	JudgeGeneratedAt  time.Time                           `json:"judge_generated_at"`
	JudgeTechnical    bool                                `json:"judge_technical_gate_passed"`
	CaseCount         int                                 `json:"case_count"`
	Agreement         evaluation.CalibrationAgreement     `json:"agreement"`
	Cases             []CaseView                          `json:"cases"`
	GovernanceVersion string                              `json:"governance_version,omitempty"`
	GovernanceSHA256  string                              `json:"governance_sha256,omitempty"`
	DatasetCard       evalgovernance.DatasetCard          `json:"dataset_card"`
	CalibrationCard   evalgovernance.JudgeCalibrationCard `json:"calibration_card"`
	Guardrails        []string                            `json:"guardrails"`
	Limitations       []string                            `json:"limitations"`
}

type ReviewReceipt struct {
	SchemaVersion string    `json:"schema_version"`
	CaseID        string    `json:"case_id"`
	Created       bool      `json:"created"`
	Revision      int       `json:"revision"`
	ReviewSHA256  string    `json:"review_sha256"`
	ReviewedAt    time.Time `json:"reviewed_at"`
}

type Service struct {
	artifacts      ArtifactStore
	governancePath string
	catalogPath    string
	repository     Repository
	clock          func() time.Time
}

func NewService(artifacts ArtifactStore, repository Repository, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{artifacts: artifacts, repository: repository, clock: clock}
}

func NewGovernedService(artifacts ArtifactStore, governancePath, catalogPath string, repository Repository, clock func() time.Time) *Service {
	service := NewService(artifacts, repository, clock)
	service.governancePath = strings.TrimSpace(governancePath)
	service.catalogPath = strings.TrimSpace(catalogPath)
	return service
}

func (service *Service) Audit(ctx context.Context, reviewer string) (Audit, error) {
	if service == nil || service.artifacts == nil || service.repository == nil || strings.TrimSpace(reviewer) == "" {
		return Audit{}, gorm.ErrInvalidDB
	}
	cases, report, err := service.artifacts.Load()
	if err != nil {
		return Audit{}, err
	}
	var governance evalgovernance.Manifest
	if service.governancePath != "" || service.catalogPath != "" {
		if service.governancePath == "" || service.catalogPath == "" {
			return Audit{}, evalgovernance.ErrInvalidGovernance
		}
		governance, err = evalgovernance.LoadBoundCatalog(service.governancePath, service.catalogPath)
		if err != nil || governance.JudgeCalibration.DatasetVersion != report.DatasetVersion || governance.JudgeCalibration.DatasetSHA256 != report.DatasetSHA256 {
			return Audit{}, evalgovernance.ErrInvalidGovernance
		}
	}
	reviews, err := service.repository.ListLatest(ctx, report.DatasetSHA256, hash(reviewer))
	if err != nil {
		return Audit{}, err
	}
	reviewByCase := make(map[string]model.JudgeCalibrationReview, len(reviews))
	ratings := make([]evaluation.HumanCalibrationRating, 0, len(reviews))
	for _, review := range reviews {
		reviewByCase[review.CaseID] = review
		ratings = append(ratings, evaluation.HumanCalibrationRating{CaseID: review.CaseID, Scores: reviewScores(review)})
	}
	agreement, err := evaluation.AnalyzeCalibrationAgreement(report, ratings)
	if err != nil {
		return Audit{}, err
	}
	judgeByCase := make(map[string]evaluation.JudgeCalibrationCaseResult, len(report.Cases))
	for _, result := range report.Cases {
		judgeByCase[result.ID] = result
	}
	views := make([]CaseView, 0, len(cases))
	for index, item := range cases {
		variant := ""
		if len(governance.JudgeCalibration.VariantOrder) == 5 {
			variant = governance.JudgeCalibration.VariantOrder[index%5]
		}
		view := CaseView{ID: item.ID, Slice: item.Slice, TaskType: item.TaskType, Question: item.Question, Answer: item.Answer, ExpectedFacts: item.ExpectedFacts, ForbiddenClaims: item.ForbiddenClaims, Judge: judgeByCase[item.ID], Evidence: evidenceViews(item.Evidence)}
		if review, exists := reviewByCase[item.ID]; exists {
			scores := reviewScores(review)
			view.HumanScores, view.HumanComment, view.ReviewRevision, view.AnswerVariant = &scores, review.Comment, review.Revision, variant
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return Audit{
		SchemaVersion: SchemaVersion, DatasetVersion: report.DatasetVersion, DatasetSHA256: report.DatasetSHA256,
		ReportSHA256: report.ReportSHA256, JudgeModel: report.ModelVersion, JudgePrompt: report.PromptVersion,
		JudgeGeneratedAt: report.GeneratedAt.UTC(), JudgeTechnical: report.TechnicalGatePassed, CaseCount: len(cases), Agreement: agreement, Cases: views,
		GovernanceVersion: governance.GovernanceVersion, GovernanceSHA256: governance.ManifestSHA256,
		DatasetCard: governance.DatasetCard, CalibrationCard: governance.JudgeCalibration,
		Guardrails:  []string{"current_reviewer_scope", "append_only_review_revisions", "fixed_dataset_and_case_hash", "kappa_gate_0.70", "no_active_policy_write"},
		Limitations: append(append(append([]string{}, report.Limitations...), governance.JudgeCalibration.Limitations...), "当前实现以登录用户作为单一复核人；增加第二位独立复核人前，不宣称双人标注一致性。"),
	}, nil
}

func (service *Service) Submit(ctx context.Context, reviewer, caseID string, scores evaluation.JudgeScores) (ReviewReceipt, error) {
	return service.SubmitWithComment(ctx, reviewer, caseID, scores, "")
}

// SubmitWithComment preserves the human rationale inside the immutable review
// commitment while keeping Submit and the existing HTTP contract compatible.
func (service *Service) SubmitWithComment(ctx context.Context, reviewer, caseID string, scores evaluation.JudgeScores, comment string) (ReviewReceipt, error) {
	if service == nil || service.artifacts == nil || service.repository == nil {
		return ReviewReceipt{}, gorm.ErrInvalidDB
	}
	reviewer, caseID, comment = strings.TrimSpace(reviewer), strings.TrimSpace(caseID), strings.TrimSpace(comment)
	if reviewer == "" || caseID == "" || invalidScores(scores) || len([]rune(comment)) > 4000 {
		return ReviewReceipt{}, ErrInvalidReview
	}
	cases, report, err := service.artifacts.Load()
	if err != nil {
		return ReviewReceipt{}, err
	}
	var selected *evaluation.JudgeCalibrationCase
	for index := range cases {
		if cases[index].ID == caseID {
			selected = &cases[index]
			break
		}
	}
	if selected == nil {
		return ReviewReceipt{}, ErrCaseNotFound
	}
	encodedCase, _ := json.Marshal(selected)
	caseDigest := sha256.Sum256(encodedCase)
	reviewerHash := hash(reviewer)
	scoreJSON, _ := json.Marshal(scores)
	reviewDigest := sha256.Sum256([]byte(ReviewSchemaVersion + "\x00" + report.DatasetSHA256 + "\x00" + caseID + "\x00" + reviewerHash + "\x00" + string(scoreJSON) + "\x00" + comment))
	now := service.clock().UTC()
	review := model.JudgeCalibrationReview{
		ID: uuid.NewString(), SchemaVersion: ReviewSchemaVersion, DatasetVersion: report.DatasetVersion, DatasetSHA256: report.DatasetSHA256,
		CaseID: caseID, CaseSHA256: hex.EncodeToString(caseDigest[:]), ReviewerHash: reviewerHash,
		Relevance: scores.Relevance, Completeness: scores.Completeness, Helpfulness: scores.Helpfulness, Groundedness: scores.Groundedness, Safety: scores.Safety,
		Overall: weightedOverall(scores), Comment: comment, ReviewSHA256: hex.EncodeToString(reviewDigest[:]), CreatedAt: now,
	}
	created, stored, err := service.repository.Append(ctx, &review)
	if err != nil {
		return ReviewReceipt{}, err
	}
	return ReviewReceipt{SchemaVersion: ReviewSchemaVersion, CaseID: stored.CaseID, Created: created, Revision: stored.Revision, ReviewSHA256: stored.ReviewSHA256, ReviewedAt: stored.CreatedAt}, nil
}

func evidenceViews(evidence []contract.Evidence) []EvidenceView {
	views := make([]EvidenceView, 0, len(evidence))
	for _, item := range evidence {
		views = append(views, EvidenceView{ID: item.ID, SourceID: item.SourceID, Origin: "embedded_synthetic_evidence", Content: item.Content})
	}
	return views
}

func reviewScores(review model.JudgeCalibrationReview) evaluation.JudgeScores {
	return evaluation.JudgeScores{Relevance: review.Relevance, Completeness: review.Completeness, Helpfulness: review.Helpfulness, Groundedness: review.Groundedness, Safety: review.Safety}
}

func invalidScores(scores evaluation.JudgeScores) bool {
	for _, value := range []float64{scores.Relevance, scores.Completeness, scores.Helpfulness, scores.Groundedness, scores.Safety} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 || math.Abs(value*4-math.Round(value*4)) > 1e-9 {
			return true
		}
	}
	return false
}

func weightedOverall(scores evaluation.JudgeScores) float64 {
	return .25*scores.Relevance + .20*scores.Completeness + .20*scores.Helpfulness + .25*scores.Groundedness + .10*scores.Safety
}

func hash(value string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(digest[:])
}
