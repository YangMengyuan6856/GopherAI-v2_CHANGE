package evaluation

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"GopherAI/internal/contract"
)

const (
	JudgeCalibrationDatasetVersion = "devsupport-judge-calibration-v1"
	JudgeCalibrationSchemaVersion  = "judge-calibration-report-v1"
	JudgeCalibrationRunnerVersion  = "judge-calibration-runner-v1"
	JudgeCalibrationCaseCount      = 30
	JudgeCalibrationKappaGate      = .70
)

var judgeCalibrationSlices = map[string]int{
	"rag_single_fact": 5, "rag_cross_document": 5, "insufficient_evidence": 5,
	"diagnosis": 5, "tool_governance": 5, "memory": 5,
}

type JudgeCalibrationCase struct {
	ID              string              `json:"id"`
	Slice           string              `json:"slice"`
	TaskType        string              `json:"task_type"`
	Question        string              `json:"question"`
	Answer          string              `json:"answer"`
	Evidence        []contract.Evidence `json:"evidence"`
	ExpectedFacts   []string            `json:"expected_facts"`
	ForbiddenClaims []string            `json:"forbidden_claims"`
	ReviewedBy      string              `json:"reviewed_by"`
	DatasetVersion  string              `json:"dataset_version"`
}

type CalibrationJudge interface {
	Judge(context.Context, JudgeInput) (JudgeResult, error)
}

type JudgeCalibrationCaseResult struct {
	ID         string      `json:"id"`
	Slice      string      `json:"slice"`
	CaseSHA256 string      `json:"case_sha256"`
	Status     string      `json:"status"`
	Attempts   int         `json:"attempts"`
	Scores     JudgeScores `json:"scores"`
	Overall    float64     `json:"overall"`
	Confidence float64     `json:"confidence"`
	ErrorCode  string      `json:"error_code,omitempty"`
}

type JudgeCalibrationReport struct {
	SchemaVersion       string                       `json:"schema_version"`
	RunnerVersion       string                       `json:"runner_version"`
	DatasetVersion      string                       `json:"dataset_version"`
	DatasetSHA256       string                       `json:"dataset_sha256"`
	AdapterVersion      string                       `json:"adapter_version"`
	PromptVersion       string                       `json:"prompt_version"`
	ModelVersion        string                       `json:"model_version"`
	GeneratedAt         time.Time                    `json:"generated_at"`
	CaseCount           int                          `json:"case_count"`
	CompletedCount      int                          `json:"completed_count"`
	FailedCount         int                          `json:"failed_count"`
	TechnicalGatePassed bool                         `json:"technical_gate_passed"`
	ReportSHA256        string                       `json:"report_sha256"`
	Cases               []JudgeCalibrationCaseResult `json:"cases"`
	Limitations         []string                     `json:"limitations"`
}

type HumanCalibrationRating struct {
	CaseID string      `json:"case_id"`
	Scores JudgeScores `json:"scores"`
}

type CalibrationAgreement struct {
	Status                 string             `json:"status"`
	RequiredCases          int                `json:"required_cases"`
	ReviewedCases          int                `json:"reviewed_cases"`
	ExactGradeAgreement    float64            `json:"exact_grade_agreement"`
	WithinOneGrade         float64            `json:"within_one_grade"`
	LinearWeightedKappa    float64            `json:"linear_weighted_kappa"`
	KappaGate              float64            `json:"kappa_gate"`
	MeanAbsoluteError      map[string]float64 `json:"mean_absolute_error"`
	CalibrationGatePassed  bool               `json:"calibration_gate_passed"`
	AutomationUsePermitted bool               `json:"automation_use_permitted"`
}

func LoadJudgeCalibrationCases(reader io.Reader) ([]JudgeCalibrationCase, error) {
	if reader == nil {
		return nil, errors.New("judge calibration dataset reader is required")
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	cases := make([]JudgeCalibrationCase, 0, JudgeCalibrationCaseCount)
	seen, slices := map[string]bool{}, map[string]int{}
	for line := 1; scanner.Scan(); line++ {
		value := strings.TrimSpace(scanner.Text())
		if value == "" {
			continue
		}
		var item JudgeCalibrationCase
		decoder := json.NewDecoder(strings.NewReader(value))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&item); err != nil {
			return nil, fmt.Errorf("decode judge calibration line %d: %w", line, err)
		}
		if err := validateJudgeCalibrationCase(item); err != nil {
			return nil, fmt.Errorf("judge calibration line %d: %w", line, err)
		}
		if seen[item.ID] {
			return nil, fmt.Errorf("judge calibration line %d: duplicate id %s", line, item.ID)
		}
		seen[item.ID], slices[item.Slice] = true, slices[item.Slice]+1
		cases = append(cases, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(cases) != JudgeCalibrationCaseCount {
		return nil, fmt.Errorf("judge calibration dataset requires %d cases, got %d", JudgeCalibrationCaseCount, len(cases))
	}
	for slice, expected := range judgeCalibrationSlices {
		if slices[slice] != expected {
			return nil, fmt.Errorf("judge calibration slice %s requires %d cases, got %d", slice, expected, slices[slice])
		}
	}
	return cases, nil
}

func RunJudgeCalibration(ctx context.Context, cases []JudgeCalibrationCase, datasetSHA string, generatedAt time.Time, judge CalibrationJudge) (JudgeCalibrationReport, error) {
	if judge == nil || len(cases) != JudgeCalibrationCaseCount || len(datasetSHA) != 64 {
		return JudgeCalibrationReport{}, errors.New("judge calibration runner input is invalid")
	}
	report := JudgeCalibrationReport{
		SchemaVersion: JudgeCalibrationSchemaVersion, RunnerVersion: JudgeCalibrationRunnerVersion,
		DatasetVersion: JudgeCalibrationDatasetVersion, DatasetSHA256: datasetSHA, AdapterVersion: JudgeAdapterVersion,
		PromptVersion: JudgePromptVersion, GeneratedAt: generatedAt.UTC(), CaseCount: len(cases),
		Cases:       make([]JudgeCalibrationCaseResult, 0, len(cases)),
		Limitations: []string{"人工复核未完成前，本报告只证明 Judge 可执行，不证明 Judge 与人工一致。", "若 Judge 与被测回答使用同一模型家族，报告必须显式披露，不可声称独立裁判。", "校准集是固定合成边界样本，不等同于真实线上分布。"},
	}
	for _, item := range cases {
		if err := ctx.Err(); err != nil {
			return JudgeCalibrationReport{}, err
		}
		caseBytes, _ := json.Marshal(item)
		caseDigest := sha256.Sum256(caseBytes)
		result, judgeErr := judge.Judge(ctx, JudgeInput{TaskType: item.TaskType, Question: item.Question, Answer: item.Answer, Evidence: item.Evidence, ExpectedFacts: item.ExpectedFacts, ForbiddenClaims: item.ForbiddenClaims})
		caseResult := JudgeCalibrationCaseResult{ID: item.ID, Slice: item.Slice, CaseSHA256: hex.EncodeToString(caseDigest[:]), Status: result.Status, Attempts: result.Attempts, Scores: result.Scores, Overall: result.Overall, Confidence: result.Confidence, ErrorCode: result.ErrorCode}
		if judgeErr != nil || result.Status != JudgeStatusComplete {
			report.FailedCount++
		} else {
			report.CompletedCount++
		}
		if report.ModelVersion == "" {
			report.ModelVersion = result.ModelVersion
		} else if result.ModelVersion != report.ModelVersion {
			return JudgeCalibrationReport{}, errors.New("judge model version changed within one calibration run")
		}
		report.Cases = append(report.Cases, caseResult)
	}
	report.TechnicalGatePassed = report.CompletedCount == JudgeCalibrationCaseCount && report.FailedCount == 0
	if err := FinalizeJudgeCalibrationReport(&report); err != nil {
		return JudgeCalibrationReport{}, err
	}
	return report, nil
}

func FinalizeJudgeCalibrationReport(report *JudgeCalibrationReport) error {
	if report == nil {
		return errors.New("judge calibration report is required")
	}
	report.ReportSHA256 = ""
	if err := ValidateJudgeCalibrationReport(*report, false); err != nil {
		return err
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	report.ReportSHA256 = hex.EncodeToString(digest[:])
	return ValidateJudgeCalibrationReport(*report, true)
}

func ValidateJudgeCalibrationReport(report JudgeCalibrationReport, requireHash bool) error {
	if report.SchemaVersion != JudgeCalibrationSchemaVersion || report.RunnerVersion != JudgeCalibrationRunnerVersion || report.DatasetVersion != JudgeCalibrationDatasetVersion || len(report.DatasetSHA256) != 64 || report.AdapterVersion != JudgeAdapterVersion || report.PromptVersion != JudgePromptVersion || strings.TrimSpace(report.ModelVersion) == "" || report.GeneratedAt.IsZero() {
		return errors.New("judge calibration report identity is invalid")
	}
	if report.CaseCount != JudgeCalibrationCaseCount || len(report.Cases) != JudgeCalibrationCaseCount || report.CompletedCount+report.FailedCount != report.CaseCount || report.TechnicalGatePassed != (report.CompletedCount == report.CaseCount && report.FailedCount == 0) {
		return errors.New("judge calibration report counts are inconsistent")
	}
	seen, slices := map[string]bool{}, map[string]int{}
	for _, item := range report.Cases {
		if strings.TrimSpace(item.ID) == "" || len(item.CaseSHA256) != 64 || seen[item.ID] || (item.Status != JudgeStatusComplete && item.Status != JudgeStatusFailed) {
			return errors.New("judge calibration case result is invalid")
		}
		if _, exists := judgeCalibrationSlices[item.Slice]; !exists {
			return errors.New("judge calibration result slice is invalid")
		}
		if item.Status == JudgeStatusComplete {
			if invalidJudgeScores(item.Scores) || mathInvalidUnit(item.Confidence) || math.Abs(item.Overall-weightedJudgeOverall(item.Scores)) > 1e-9 {
				return errors.New("completed judge calibration score is invalid")
			}
		} else if strings.TrimSpace(item.ErrorCode) == "" {
			return errors.New("failed judge calibration result requires an error code")
		}
		seen[item.ID] = true
		slices[item.Slice]++
	}
	for slice, expected := range judgeCalibrationSlices {
		if slices[slice] != expected {
			return errors.New("judge calibration report slice counts are inconsistent")
		}
	}
	if requireHash {
		supplied := report.ReportSHA256
		report.ReportSHA256 = ""
		encoded, _ := json.Marshal(report)
		digest := sha256.Sum256(encoded)
		if supplied != hex.EncodeToString(digest[:]) {
			return errors.New("judge calibration report hash is invalid")
		}
	}
	return nil
}

func AnalyzeCalibrationAgreement(report JudgeCalibrationReport, ratings []HumanCalibrationRating) (CalibrationAgreement, error) {
	if err := ValidateJudgeCalibrationReport(report, true); err != nil {
		return CalibrationAgreement{}, err
	}
	result := CalibrationAgreement{Status: "insufficient_human_review", RequiredCases: JudgeCalibrationCaseCount, KappaGate: JudgeCalibrationKappaGate, MeanAbsoluteError: map[string]float64{"relevance": 0, "completeness": 0, "helpfulness": 0, "groundedness": 0, "safety": 0}}
	judgeByID := make(map[string]JudgeCalibrationCaseResult, len(report.Cases))
	for _, item := range report.Cases {
		judgeByID[item.ID] = item
	}
	seen := map[string]bool{}
	judgeGrades, humanGrades := []int{}, []int{}
	for _, rating := range ratings {
		judgeResult, exists := judgeByID[rating.CaseID]
		if !exists || seen[rating.CaseID] || invalidJudgeScores(rating.Scores) {
			return CalibrationAgreement{}, errors.New("human calibration rating is invalid or duplicated")
		}
		seen[rating.CaseID] = true
		if judgeResult.Status != JudgeStatusComplete {
			continue
		}
		result.ReviewedCases++
		judgeGrades = append(judgeGrades, scoreGrade(judgeResult.Overall))
		humanOverall := weightedJudgeOverall(rating.Scores)
		humanGrades = append(humanGrades, scoreGrade(humanOverall))
		result.MeanAbsoluteError["relevance"] += math.Abs(judgeResult.Scores.Relevance - rating.Scores.Relevance)
		result.MeanAbsoluteError["completeness"] += math.Abs(judgeResult.Scores.Completeness - rating.Scores.Completeness)
		result.MeanAbsoluteError["helpfulness"] += math.Abs(judgeResult.Scores.Helpfulness - rating.Scores.Helpfulness)
		result.MeanAbsoluteError["groundedness"] += math.Abs(judgeResult.Scores.Groundedness - rating.Scores.Groundedness)
		result.MeanAbsoluteError["safety"] += math.Abs(judgeResult.Scores.Safety - rating.Scores.Safety)
	}
	if result.ReviewedCases == 0 {
		return result, nil
	}
	for key, value := range result.MeanAbsoluteError {
		result.MeanAbsoluteError[key] = math.Round(value/float64(result.ReviewedCases)*10000) / 10000
	}
	exact, within := 0, 0
	for index := range judgeGrades {
		difference := judgeGrades[index] - humanGrades[index]
		if difference == 0 {
			exact++
		}
		if difference >= -1 && difference <= 1 {
			within++
		}
	}
	result.ExactGradeAgreement = float64(exact) / float64(result.ReviewedCases)
	result.WithinOneGrade = float64(within) / float64(result.ReviewedCases)
	result.LinearWeightedKappa = linearWeightedKappa(judgeGrades, humanGrades, 5)
	if result.ReviewedCases == JudgeCalibrationCaseCount && report.TechnicalGatePassed {
		result.Status = "calibrated"
		result.CalibrationGatePassed = result.LinearWeightedKappa >= JudgeCalibrationKappaGate
		if !result.CalibrationGatePassed {
			result.Status = "rubric_revision_required"
		}
	}
	result.AutomationUsePermitted = result.CalibrationGatePassed
	return result, nil
}

func validateJudgeCalibrationCase(item JudgeCalibrationCase) error {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Slice) == "" || item.DatasetVersion != JudgeCalibrationDatasetVersion || item.ReviewedBy != "pending_user" {
		return errors.New("case identity, slice, review state or dataset version is invalid")
	}
	if _, exists := judgeCalibrationSlices[item.Slice]; !exists {
		return errors.New("case slice is not allowlisted")
	}
	return validateJudgeInput(JudgeInput{TaskType: item.TaskType, Question: item.Question, Answer: item.Answer, Evidence: item.Evidence, ExpectedFacts: item.ExpectedFacts, ForbiddenClaims: item.ForbiddenClaims})
}

func invalidJudgeScores(scores JudgeScores) bool {
	for _, value := range []float64{scores.Relevance, scores.Completeness, scores.Helpfulness, scores.Groundedness, scores.Safety} {
		if mathInvalidUnit(value) {
			return true
		}
	}
	return false
}

func scoreGrade(score float64) int {
	grade := int(math.Round(score * 4))
	if grade < 0 {
		return 0
	}
	if grade > 4 {
		return 4
	}
	return grade
}

func linearWeightedKappa(left []int, right []int, categories int) float64 {
	if len(left) == 0 || len(left) != len(right) || categories < 2 {
		return 0
	}
	observed, leftCounts, rightCounts := 0.0, make([]float64, categories), make([]float64, categories)
	for index := range left {
		weight := 1 - float64(absInt(left[index]-right[index]))/float64(categories-1)
		observed += weight
		leftCounts[left[index]]++
		rightCounts[right[index]]++
	}
	observed /= float64(len(left))
	expected := 0.0
	for i := 0; i < categories; i++ {
		for j := 0; j < categories; j++ {
			weight := 1 - float64(absInt(i-j))/float64(categories-1)
			expected += weight * leftCounts[i] * rightCounts[j] / float64(len(left)*len(left))
		}
	}
	if math.Abs(1-expected) < 1e-12 {
		if math.Abs(1-observed) < 1e-12 {
			return 1
		}
		return 0
	}
	return math.Round((observed-expected)/(1-expected)*10000) / 10000
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func SortedJudgeCalibrationSlices() []string {
	values := make([]string, 0, len(judgeCalibrationSlices))
	for value := range judgeCalibrationSlices {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
