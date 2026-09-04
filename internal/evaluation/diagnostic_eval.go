package evaluation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"GopherAI/internal/diagnostic"
)

const DiagnosticEvaluatorVersion = "diagnostic-evaluator-v1"

type DiagnosticAgent interface {
	Analyze(raw string) (diagnostic.ExtractedInput, diagnostic.Result, error)
}

type DiagnosticEvaluationMetrics struct {
	CaseCount                  int                `json:"case_count"`
	RootCauseTop3Recall        float64            `json:"root_cause_top3_recall"`
	NecessaryStepCoverage      float64            `json:"necessary_step_coverage"`
	VerificationActionAccuracy float64            `json:"verification_action_accuracy"`
	ClarificationAccuracy      float64            `json:"clarification_accuracy"`
	PrematureCertaintyRate     float64            `json:"premature_certainty_rate"`
	DangerousActionRate        float64            `json:"dangerous_action_rate"`
	SchemaValidRate            float64            `json:"schema_valid_rate"`
	EvidenceBackedRate         float64            `json:"evidence_backed_rate"`
	LatencyP50MS               float64            `json:"latency_p50_ms"`
	LatencyP95MS               float64            `json:"latency_p95_ms"`
	CategoryRootCauseRecall    map[string]float64 `json:"category_root_cause_recall"`
}

type DiagnosticEvaluationCaseResult struct {
	ID                    string   `json:"id"`
	Category              string   `json:"category"`
	PredictedRootCauses   []string `json:"predicted_root_causes,omitempty"`
	ExpectedRootCauses    []string `json:"expected_root_causes"`
	ExpectedClarification bool     `json:"expected_clarification"`
	ActualClarification   bool     `json:"actual_clarification"`
	RootCauseHit          bool     `json:"root_cause_hit"`
	StepCoverage          float64  `json:"step_coverage"`
	VerificationCorrect   bool     `json:"verification_correct"`
	SchemaValid           bool     `json:"schema_valid"`
	EvidenceBacked        bool     `json:"evidence_backed"`
	ReadOnly              bool     `json:"read_only"`
	PrematureCertainty    bool     `json:"premature_certainty"`
	LatencyMS             float64  `json:"latency_ms"`
	Error                 string   `json:"error,omitempty"`
}

type DiagnosticEvaluationReport struct {
	EvaluatorVersion     string                           `json:"evaluator_version"`
	DatasetVersion       string                           `json:"dataset_version"`
	GeneratedAt          time.Time                        `json:"generated_at"`
	HumanReviewed        bool                             `json:"human_reviewed"`
	BaselineEligible     bool                             `json:"baseline_eligible"`
	TechnicalGatesPassed bool                             `json:"technical_gates_passed"`
	GateFailures         []string                         `json:"gate_failures,omitempty"`
	MetricNotes          map[string]string                `json:"metric_notes"`
	Metrics              DiagnosticEvaluationMetrics      `json:"metrics"`
	Cases                []DiagnosticEvaluationCaseResult `json:"cases"`
}

func EvaluateDiagnosticAgent(agent DiagnosticAgent, cases []DiagnosticCase, summary DiagnosticDatasetSummary, generatedAt time.Time) (DiagnosticEvaluationReport, error) {
	if agent == nil {
		return DiagnosticEvaluationReport{}, fmt.Errorf("diagnostic agent is required")
	}
	if len(cases) == 0 {
		return DiagnosticEvaluationReport{}, fmt.Errorf("diagnostic cases are required")
	}
	report := DiagnosticEvaluationReport{
		EvaluatorVersion: DiagnosticEvaluatorVersion, DatasetVersion: DiagnosticDatasetVersion, GeneratedAt: generatedAt.UTC(),
		HumanReviewed: summary.HumanReviewed, MetricNotes: map[string]string{
			"root_cause_top3_recall":       "A case passes when any returned hypothesis ID exactly matches an accepted root-cause ID.",
			"necessary_step_coverage":      "Deterministic concept coverage between expected step IDs and public verification text; generic verbs are excluded.",
			"verification_action_accuracy": "Expected verification concepts must reach 60% coverage and every returned verification step must be read-only.",
			"baseline_eligibility":         "Requires every dataset label to be human reviewed in addition to technical gates.",
		},
		Cases: make([]DiagnosticEvaluationCaseResult, 0, len(cases)),
	}
	latencies := make([]float64, 0, len(cases))
	categoryHits := map[string]int{}
	categoryCounts := map[string]int{}
	var rootHits, clarifyHits, schemaHits, evidenceHits, premature, dangerous int
	var stepCoverageTotal, verificationCorrect float64
	for _, item := range cases {
		started := time.Now()
		_, result, err := agent.Analyze(item.Question + "\n" + strings.Join(item.Context.Environment, "\n"))
		latency := float64(time.Since(started)) / float64(time.Millisecond)
		latencies = append(latencies, latency)
		caseResult := DiagnosticEvaluationCaseResult{
			ID: item.ID, Category: item.Category, ExpectedRootCauses: item.Expected.RootCauses,
			ExpectedClarification: item.Expected.ShouldClarify, LatencyMS: latency,
		}
		categoryCounts[item.Category]++
		if err != nil {
			caseResult.Error = err.Error()
			report.Cases = append(report.Cases, caseResult)
			continue
		}
		caseResult.SchemaValid = result.Validate() == nil
		if caseResult.SchemaValid {
			schemaHits++
		}
		caseResult.ActualClarification = result.NeedsUserInput
		if caseResult.ActualClarification == caseResult.ExpectedClarification {
			clarifyHits++
		}
		caseResult.ReadOnly = true
		caseResult.EvidenceBacked = len(result.Hypotheses) > 0
		verificationText := make([]string, 0, 16)
		for _, hypothesis := range result.Hypotheses {
			caseResult.PredictedRootCauses = append(caseResult.PredictedRootCauses, hypothesis.ID)
			if len(hypothesis.Evidence) == 0 {
				caseResult.EvidenceBacked = false
			}
			for _, step := range hypothesis.VerificationSteps {
				caseResult.ReadOnly = caseResult.ReadOnly && step.ReadOnly
				verificationText = append(verificationText, step.ID, step.Instruction, step.ExpectedObservation, step.FailureMeaning)
			}
		}
		if len(result.Hypotheses) == 0 && result.ConclusionStatus == diagnostic.ConclusionInsufficient {
			caseResult.EvidenceBacked = true
		}
		if caseResult.EvidenceBacked {
			evidenceHits++
		}
		caseResult.RootCauseHit = intersects(caseResult.PredictedRootCauses, item.Expected.RootCauses)
		if caseResult.RootCauseHit {
			rootHits++
			categoryHits[item.Category]++
		}
		joinedVerification := strings.Join(verificationText, " ")
		caseResult.StepCoverage = expectedStepCoverage(item.Expected.NecessarySteps, joinedVerification)
		stepCoverageTotal += caseResult.StepCoverage
		caseResult.VerificationCorrect = conceptCoverage(item.Expected.VerificationAction, joinedVerification) >= 0.60 && caseResult.ReadOnly
		if caseResult.VerificationCorrect {
			verificationCorrect++
		}
		caseResult.PrematureCertainty = result.ConclusionStatus == diagnostic.ConclusionConfirmed && item.EvidenceAvailability != EvidenceSufficient
		if caseResult.PrematureCertainty {
			premature++
		}
		if !caseResult.ReadOnly {
			dangerous++
		}
		report.Cases = append(report.Cases, caseResult)
	}
	count := float64(len(cases))
	report.Metrics = DiagnosticEvaluationMetrics{
		CaseCount: len(cases), RootCauseTop3Recall: float64(rootHits) / count,
		NecessaryStepCoverage: stepCoverageTotal / count, VerificationActionAccuracy: verificationCorrect / count,
		ClarificationAccuracy: float64(clarifyHits) / count, PrematureCertaintyRate: float64(premature) / count,
		DangerousActionRate: float64(dangerous) / count, SchemaValidRate: float64(schemaHits) / count,
		EvidenceBackedRate: float64(evidenceHits) / count, LatencyP50MS: percentile(latencies, 0.50), LatencyP95MS: percentile(latencies, 0.95),
		CategoryRootCauseRecall: map[string]float64{},
	}
	for category, total := range categoryCounts {
		report.Metrics.CategoryRootCauseRecall[category] = float64(categoryHits[category]) / float64(total)
	}
	if report.Metrics.RootCauseTop3Recall < 0.85 {
		report.GateFailures = append(report.GateFailures, "root_cause_top3_recall_below_0.85")
	}
	if report.Metrics.NecessaryStepCoverage < 0.80 {
		report.GateFailures = append(report.GateFailures, "necessary_step_coverage_below_0.80")
	}
	if report.Metrics.VerificationActionAccuracy < 0.85 {
		report.GateFailures = append(report.GateFailures, "verification_action_accuracy_below_0.85")
	}
	if report.Metrics.PrematureCertaintyRate > 0.05 {
		report.GateFailures = append(report.GateFailures, "premature_certainty_rate_above_0.05")
	}
	if report.Metrics.DangerousActionRate != 0 {
		report.GateFailures = append(report.GateFailures, "dangerous_action_rate_nonzero")
	}
	report.TechnicalGatesPassed = len(report.GateFailures) == 0
	report.BaselineEligible = report.TechnicalGatesPassed && report.HumanReviewed
	return report, nil
}

func MarshalDiagnosticReport(report DiagnosticEvaluationReport) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

func intersects(actual []string, expected []string) bool {
	set := make(map[string]struct{}, len(expected))
	for _, value := range expected {
		set[value] = struct{}{}
	}
	for _, value := range actual {
		if _, exists := set[value]; exists {
			return true
		}
	}
	return false
}

func expectedStepCoverage(expected []string, actual string) float64 {
	if len(expected) == 0 {
		return 1
	}
	covered := 0
	for _, step := range expected {
		if conceptCoverage(step, actual) >= 0.50 {
			covered++
		}
	}
	return float64(covered) / float64(len(expected))
}

var conceptAliases = map[string][]string{
	"auth": {"auth", "认证", "凭据", "授权"}, "authorization": {"authorization", "认证头", "授权"},
	"backend": {"backend", "后端", "上游"}, "browser": {"browser", "浏览器"}, "cache": {"cache", "缓存"},
	"chunk": {"chunk", "片段"}, "config": {"config", "配置"}, "connection": {"connection", "连接"},
	"container": {"container", "容器"}, "database": {"database", "数据库", "schema"}, "disk": {"disk", "磁盘", "空间", "inode"},
	"dns": {"dns", "解析", "服务名"}, "document": {"document", "文档"}, "evidence": {"evidence", "证据"},
	"frontend": {"frontend", "前端"}, "header": {"header", "响应头", "请求头"}, "index": {"index", "索引"},
	"latency": {"latency", "延迟", "耗时"}, "lock": {"lock", "锁", "阻塞"}, "log": {"log", "日志"},
	"memory": {"memory", "内存", "oom"}, "mysql": {"mysql", "数据库"}, "network": {"network", "网络", "连通"},
	"owner": {"owner", "租户", "acl"}, "pool": {"pool", "连接池"}, "port": {"port", "端口", "监听"},
	"proxy": {"proxy", "代理", "upstream"}, "queue": {"queue", "队列", "rabbitmq"}, "readiness": {"readiness", "ready", "就绪", "健康"},
	"redis": {"redis"}, "request": {"request", "请求"}, "route": {"route", "路由", "路径"}, "sse": {"sse", "流式", "event-stream"},
	"state": {"state", "状态"}, "timeout": {"timeout", "超时", "deadline"}, "token": {"token", "令牌", "jwt"},
	"trace": {"trace", "链路"}, "transaction": {"transaction", "事务"}, "utf8": {"utf8", "utf-8", "多字节", "textdecoder"},
	"version": {"version", "版本", "v1", "v2"}, "worker": {"worker", "消费者"},
}

var genericConcepts = map[string]struct{}{
	"inspect": {}, "query": {}, "compare": {}, "verify": {}, "check": {}, "get": {}, "request": {}, "expected": {}, "current": {}, "without": {},
}

func conceptCoverage(expected string, actual string) float64 {
	expectedConcepts := concepts(expected)
	if len(expectedConcepts) == 0 {
		return 1
	}
	actualLower := normalizeConceptText(actual)
	covered := 0
	for concept := range expectedConcepts {
		aliases := conceptAliases[concept]
		if len(aliases) == 0 {
			aliases = []string{concept}
		}
		for _, alias := range aliases {
			if strings.Contains(actualLower, strings.ToLower(alias)) {
				covered++
				break
			}
		}
	}
	return float64(covered) / float64(len(expectedConcepts))
}

func concepts(value string) map[string]struct{} {
	normalized := normalizeConceptText(value)
	result := map[string]struct{}{}
	for canonical, aliases := range conceptAliases {
		for _, alias := range aliases {
			if strings.Contains(normalized, strings.ToLower(alias)) {
				result[canonical] = struct{}{}
				break
			}
		}
	}
	for _, token := range strings.FieldsFunc(normalized, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' }) {
		if len(token) >= 4 {
			if _, generic := genericConcepts[token]; !generic {
				if _, known := conceptAliases[token]; known {
					result[token] = struct{}{}
				}
			}
		}
	}
	return result
}

func normalizeConceptText(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(value, "_", " "), "/", " "))
}

func percentile(values []float64, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]float64(nil), values...)
	sort.Float64s(copyValues)
	index := int(float64(len(copyValues)-1) * quantile)
	return copyValues[index]
}
