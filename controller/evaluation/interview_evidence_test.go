package evaluation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/controlrecommendation"
	evaldomain "GopherAI/internal/evaluation"
	"GopherAI/internal/faultcampaign"
	"GopherAI/internal/judgecalibration"
	"GopherAI/internal/observability"
	"GopherAI/internal/perfeval"
	"GopherAI/internal/reliabilityeval"

	"github.com/gin-gonic/gin"
)

func TestBuildInterviewEvidencePackagePreservesProvenanceAndHumanBlockers(t *testing.T) {
	input := validInterviewEvidenceInputs(t)
	report, err := buildInterviewEvidencePackage(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.PackageSHA256) != 64 || !report.AllSourcesVerified || report.ResumeReadyClaims != 6 || report.TotalClaims != 10 || len(report.Sources) != 12 {
		t.Fatalf("unexpected evidence package: %+v", report)
	}
	byID := map[string]InterviewEvidenceStatement{}
	for _, statement := range report.Statements {
		byID[statement.ID] = statement
	}
	if byID["bounded_ecs_performance"].Status != "resume_ready" || !byID["bounded_ecs_performance"].ResumeMetricEligible {
		t.Fatalf("bounded real performance evidence should be ready: %+v", byID["bounded_ecs_performance"])
	}
	if byID["multi_agent_paired_gain"].ResumeMetricEligible || byID["judge_human_calibration"].Status != "calibration_pending" || byID["parent_context_negative_result"].Status != "negative_result" {
		t.Fatalf("human gates or negative result were lost: %+v", byID)
	}
	for _, id := range []string{"observe_only_fault_campaign", "agent_recovery_and_sse_cancel", "bounded_metric_catalog", "private_grafana_dashboard", "recommend_only_control_guard"} {
		if !byID[id].ResumeMetricEligible {
			t.Fatalf("expected verified operational claim %s: %+v", id, byID[id])
		}
	}
	encoded, _ := json.Marshal(report)
	for _, forbidden := range []string{"OPENAI_API_KEY", "Bearer ", "question\"", "answer\""} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("evidence package leaked forbidden content %q", forbidden)
		}
	}
	var markdown bytes.Buffer
	if err := writeInterviewEvidenceMarkdown(&markdown, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markdown.String(), report.PackageSHA256) || !strings.Contains(markdown.String(), "禁止夸大") || !strings.Contains(markdown.String(), "6/10") {
		t.Fatalf("markdown export is incomplete: %s", markdown.String())
	}
}

func TestBuildInterviewEvidencePackageRejectsUnboundSource(t *testing.T) {
	input := validInterviewEvidenceInputs(t)
	input.ReleaseSHA = "bad"
	if _, err := buildInterviewEvidencePackage(input); err == nil {
		t.Fatal("expected invalid release source hash rejection")
	}
}

type interviewEvidenceServiceStub struct {
	report InterviewEvidencePackage
	err    error
}

func (stub interviewEvidenceServiceStub) Build(context.Context, string) (InterviewEvidencePackage, error) {
	return stub.report, stub.err
}

func TestInterviewEvidenceHandlerExportsMarkdownAndRejectsUnknownFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	report, err := buildInterviewEvidencePackage(validInterviewEvidenceInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	handler := NewInterviewEvidenceHandler(interviewEvidenceServiceStub{report: report})

	markdownRecorder := httptest.NewRecorder()
	markdownContext, _ := gin.CreateTestContext(markdownRecorder)
	markdownContext.Set("userName", "reviewer")
	markdownContext.Request = httptest.NewRequest(http.MethodGet, "/evaluations/interview-evidence/latest?format=markdown", nil)
	handler.Latest(markdownContext)
	if markdownRecorder.Code != http.StatusOK || !strings.HasPrefix(markdownRecorder.Header().Get("Content-Type"), "text/markdown") || !strings.Contains(markdownRecorder.Body.String(), report.PackageSHA256) {
		t.Fatalf("unexpected markdown export: status=%d header=%v body=%s", markdownRecorder.Code, markdownRecorder.Header(), markdownRecorder.Body.String())
	}

	badRecorder := httptest.NewRecorder()
	badContext, _ := gin.CreateTestContext(badRecorder)
	badContext.Request = httptest.NewRequest(http.MethodGet, "/evaluations/interview-evidence/latest?format=html", nil)
	handler.Latest(badContext)
	if badRecorder.Code != http.StatusBadRequest || !strings.Contains(badRecorder.Body.String(), "INVALID_EVIDENCE_FORMAT") {
		t.Fatalf("unexpected invalid format response: status=%d body=%s", badRecorder.Code, badRecorder.Body.String())
	}
}

func validInterviewEvidenceInputs(t *testing.T) interviewEvidenceInputs {
	t.Helper()
	unified, unifiedSHA, err := NewFileUnifiedReportStore("../../evals/results/devsupport-eval-run-v1-candidate.json").Load()
	if err != nil {
		t.Fatal(err)
	}
	paired, err := buildPairedSummary(validPairedCollaborationReport(), strings.Repeat("c", 64), validPairedParentReport(), strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	performance := validInterviewPerformanceReport(t)
	judgeCases := make([]judgecalibration.CaseView, evaldomain.JudgeCalibrationCaseCount)
	for index := range judgeCases {
		judgeCases[index] = judgecalibration.CaseView{ID: fmt.Sprintf("judge-%02d", index+1), Judge: evaldomain.JudgeCalibrationCaseResult{Status: evaldomain.JudgeStatusComplete}}
	}
	faultReport, err := faultcampaign.BuildReport()
	if err != nil {
		t.Fatal(err)
	}
	reliabilityReport, err := reliabilityeval.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	metricHandler := NewDefaultMetricCatalogHandler()
	if metricHandler.err != nil {
		t.Fatal(metricHandler.err)
	}
	grafana := observability.GrafanaRuntimeSnapshot{
		SchemaVersion: observability.GrafanaRuntimeSchemaVersion, Status: "ready", Source: "test", CollectedAt: time.Unix(40, 0).UTC(),
		GrafanaVersion: "13.2.1", Database: "ok", BindAddress: "container-private:9093", PublicExposure: false,
		Dashboard: observability.GrafanaDashboardContract{SchemaVersion: observability.GrafanaDashboardSchemaVersion, UID: observability.GrafanaDashboardUID, DashboardSHA: strings.Repeat("9", 64), PanelCount: 22, QueryCount: 22, Groups: []observability.GrafanaDashboardGroup{{Title: "业务", PanelCount: 7}, {Title: "质量", PanelCount: 8}, {Title: "控制", PanelCount: 7}}, Passed: true},
	}
	control := controlrecommendation.AuditSnapshot{
		SchemaVersion: controlrecommendation.SchemaVersion, Mode: controlrecommendation.ModeRecommendOnly, Recommended: 1, Blocked: 1,
		ActivePolicy: controlrecommendation.PolicyIdentity{Version: "policy-v1", SHA256: strings.Repeat("8", 64), Status: "active"},
		Evaluation:   controlrecommendation.EvaluationGate{Source: "test", RunID: "run", CandidateVersion: "candidate", ReportSHA256: strings.Repeat("7", 64)},
		Latest: []controlrecommendation.RecommendationSummary{
			{RecommendationID: "one", Status: controlrecommendation.StatusRecommended, Simulation: true, Applied: false, CreatedAt: time.Unix(50, 0).UTC()},
			{RecommendationID: "two", Status: controlrecommendation.StatusBlocked, Simulation: true, Applied: false, CreatedAt: time.Unix(51, 0).UTC()},
		},
	}
	controlSHA, err := digestInterviewEvidenceValue(control)
	if err != nil {
		t.Fatal(err)
	}
	return interviewEvidenceInputs{
		Release: interviewReleaseManifest{
			ReleaseID: "release-current", Branch: "add_eico", GitSHA: strings.Repeat("a", 40), BuiltAt: time.Unix(10, 0).UTC(),
			BuildStrategy: "local-linux-amd64-nocgo", Target: "linux/amd64", IncludedComponents: []string{"backend", "worker", "mcp", "frontend", "eval"},
		},
		ReleaseSHA: strings.Repeat("b", 64), Unified: unified, UnifiedSHA: unifiedSHA, Paired: paired, Performance: performance,
		JudgeCalibration: judgecalibration.Audit{
			SchemaVersion: judgecalibration.SchemaVersion, ReportSHA256: strings.Repeat("e", 64), JudgePrompt: evaldomain.JudgePromptVersion,
			JudgeModel: "judge-model", JudgeGeneratedAt: time.Unix(30, 0).UTC(), JudgeTechnical: true, CaseCount: evaldomain.JudgeCalibrationCaseCount,
			Agreement: evaldomain.CalibrationAgreement{Status: "insufficient_human_review", RequiredCases: 30, ReviewedCases: 0, KappaGate: .70},
			Cases:     judgeCases,
		},
		FaultCampaign: faultReport, FaultGeneratedAt: time.Unix(35, 0).UTC(), Reliability: reliabilityReport,
		MetricCatalog: metricHandler.report, Grafana: grafana, Control: control, ControlSHA: controlSHA,
	}
}

func validInterviewPerformanceReport(t *testing.T) perfeval.Report {
	t.Helper()
	profileRoot := "/root/GopherAI_Runtime/perf/release-1/"
	report := perfeval.Report{
		SchemaVersion: perfeval.SchemaVersion, RunnerVersion: perfeval.RunnerVersion, GeneratedAt: time.Unix(20, 0).UTC(), Mode: "bounded_loopback_acceptance",
		Release: perfeval.Release{ID: "release-1", GitSHA: strings.Repeat("f", 40), BuildStrategy: "local"},
		Runtime: perfeval.Runtime{Target: "http://127.0.0.1:9090/api/v1/chat/auto/stream", GoVersion: "go1.test", CPUCores: 2, MemoryTotalBytes: 1},
		Cold:    perfeval.NewPhase("release_first_request", "first", 1, 1, 0, []float64{10}, []float64{5}, 1, 10),
		Hot:     perfeval.NewPhase("warmed_route", "warm", 10, 2, 1, []float64{8, 9, 10, 11, 12, 13, 14, 15, 16, 17}, []float64{3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, 10, 100),
		Profiles: []perfeval.ProfileArtifact{
			{Kind: "cpu", Path: profileRoot + "cpu.pprof", Bytes: 1, SHA256: strings.Repeat("1", 64)},
			{Kind: "heap", Path: profileRoot + "heap.pprof", Bytes: 1, SHA256: strings.Repeat("2", 64)},
			{Kind: "goroutine", Path: profileRoot + "goroutine.txt", Bytes: 1, SHA256: strings.Repeat("3", 64)},
		},
		Gates:      perfeval.Gates{ColdEligible: true, AllRequestsPassed: true, ProfilesCaptured: true, SyntheticDataCleaned: true, TechnicalPassed: true},
		Guardrails: []string{"a", "b", "c", "d"}, Limitations: []string{"one", "two"},
	}
	report.Cold.DurationMS, report.Hot.DurationMS = 10, 125
	report.Cold.ObservedRoutes = []perfeval.Route{{Strategy: "legacy_chat", StrategyVersion: "v1", PolicyVersion: "p1"}}
	report.Hot.ObservedRoutes = []perfeval.Route{{Strategy: "legacy_chat", StrategyVersion: "v1", PolicyVersion: "p1"}}
	if err := perfeval.Finalize(&report); err != nil {
		t.Fatal(err)
	}
	return report
}
