package evaluation

import (
	knowledgeagent "GopherAI/internal/agent/knowledge"
	"GopherAI/internal/contract"
	"GopherAI/internal/rag"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

type evaluationSearcher struct {
	outputs map[string]rag.SearchOutput
}

func (searcher *evaluationSearcher) Search(_ context.Context, input rag.SearchInput) (rag.SearchOutput, error) {
	return searcher.outputs[input.Query], nil
}

type evaluationAnswerer struct {
	outputs map[string]knowledgeagent.Output
}

func (answerer *evaluationAnswerer) Answer(_ context.Context, input knowledgeagent.Input) (knowledgeagent.Output, error) {
	return answerer.outputs[input.Question], nil
}

func TestRAGCoreScoringAndReport(t *testing.T) {
	cases := []RAGCase{
		{ID: "rag-001", Question: "q1", Expected: RAGExpected{EvidenceIDs: []string{"a"}}},
		{ID: "rag-002", Question: "q2", Expected: RAGExpected{EvidenceIDs: []string{"b", "c"}}},
	}
	searcher := &evaluationSearcher{outputs: map[string]rag.SearchOutput{
		"q1": {Hits: []rag.SearchHit{{Evidence: contract.Evidence{ID: "a", TenantID: "tenant", SourceID: "doc"}}}},
		"q2": {Hits: []rag.SearchHit{{Evidence: contract.Evidence{ID: "x", TenantID: "tenant", SourceID: "doc"}}, {Evidence: contract.Evidence{ID: "b", TenantID: "tenant", SourceID: "doc"}}, {Evidence: contract.Evidence{ID: "c", TenantID: "tenant", SourceID: "doc"}}}},
	}}
	answerer := &evaluationAnswerer{outputs: map[string]knowledgeagent.Output{
		"q1": {Result: contract.AgentResult{Resolved: true, Citations: []contract.Citation{{EvidenceID: "a"}}}, Gate: rag.EvidenceGateResult{Accepted: true}},
		"q2": {Result: contract.AgentResult{Resolved: true, Citations: []contract.Citation{{EvidenceID: "b"}, {EvidenceID: "c"}}}, Gate: rag.EvidenceGateResult{Accepted: true}},
	}}
	progressCalls := 0
	report := RunRAGCoreWithObserver(context.Background(), cases, "fixture", "candidate", "tenant", "user", searcher, answerer,
		func(completed int, total int, _ RAGCaseResult) {
			progressCalls++
			if completed != progressCalls || total != len(cases) {
				t.Fatalf("unexpected progress: completed=%d total=%d", completed, total)
			}
		})
	if report.Metrics.RecallAt5 != 1 || report.Metrics.CitationPrecision != 1 || report.Metrics.CitationCoverage != 1 || report.Metrics.UnauthorizedRecall != 0 {
		t.Fatalf("unexpected metrics: %+v", report.Metrics)
	}
	if report.Metrics.NDCGAt5 >= 1 || report.Metrics.MRR >= 1 {
		t.Fatalf("ranking penalty was not applied: %+v", report.Metrics)
	}
	if progressCalls != len(cases) {
		t.Fatalf("expected progress for every case, got %d", progressCalls)
	}
	if report.HumanReviewed || report.BaselineEligible {
		t.Fatalf("unreviewed labels must not be baseline eligible: %+v", report)
	}
	var markdown strings.Builder
	if err := WriteRAGReportMarkdown(&markdown, report); err != nil || !strings.Contains(markdown.String(), "Citation Precision") {
		t.Fatalf("unexpected markdown report: %v %s", err, markdown.String())
	}
}

func TestRAGCoreCitationPrecisionExcludesUnresolvedSafetyEvidence(t *testing.T) {
	cases := []RAGCase{
		{ID: "resolved", Question: "q1", Expected: RAGExpected{EvidenceIDs: []string{"a"}}},
		{ID: "unresolved", Question: "q2", Expected: RAGExpected{EvidenceIDs: []string{"b"}}},
	}
	searcher := &evaluationSearcher{outputs: map[string]rag.SearchOutput{
		"q1": {Hits: []rag.SearchHit{{Evidence: contract.Evidence{ID: "a", TenantID: "tenant", SourceID: "doc"}}}},
		"q2": {Hits: []rag.SearchHit{{Evidence: contract.Evidence{ID: "b", TenantID: "tenant", SourceID: "doc"}}}},
	}}
	answerer := &evaluationAnswerer{outputs: map[string]knowledgeagent.Output{
		"q1": {Result: contract.AgentResult{Resolved: true, Citations: []contract.Citation{{EvidenceID: "a"}}}, Gate: rag.EvidenceGateResult{Accepted: true}},
		"q2": {Result: contract.AgentResult{Resolved: false, Citations: []contract.Citation{{EvidenceID: "b"}, {EvidenceID: "unclaimed-context"}}}, Gate: rag.EvidenceGateResult{Accepted: true}},
	}}
	report := RunRAGCore(context.Background(), cases, "fixture", "candidate", "tenant", "user", searcher, answerer)
	if report.Metrics.CitationPrecision != 1 {
		t.Fatalf("unresolved safety evidence must not reduce citation precision: %+v", report.Metrics)
	}
	if report.Metrics.CitationCoverage != 0.5 || report.Metrics.ResolvedAnswerRate != 0.5 {
		t.Fatalf("unresolved answer must remain visible in coverage and resolved rate: %+v", report.Metrics)
	}
}

func TestRAGCoreSeparatesPositiveRetrievalFromNoEvidenceSafety(t *testing.T) {
	resolve := true
	reject := false
	cases := []RAGCase{
		{ID: "positive", Question: "known", Expected: RAGExpected{EvidenceIDs: []string{"a"}, ShouldResolve: &resolve}},
		{ID: "negative", Question: "unknown", Expected: RAGExpected{ShouldResolve: &reject}},
	}
	searcher := &evaluationSearcher{outputs: map[string]rag.SearchOutput{
		"known":   {Hits: []rag.SearchHit{{Evidence: contract.Evidence{ID: "a", TenantID: "tenant", SourceID: "doc"}}}},
		"unknown": {},
	}}
	answerer := &evaluationAnswerer{outputs: map[string]knowledgeagent.Output{
		"known":   {Result: contract.AgentResult{Resolved: true, Citations: []contract.Citation{{EvidenceID: "a"}}}, Gate: rag.EvidenceGateResult{Accepted: true}},
		"unknown": {Result: contract.AgentResult{Resolved: false}, Gate: rag.EvidenceGateResult{Accepted: false, ReasonCode: rag.GateReasonNoEvidence}},
	}}
	report := RunRAGCore(context.Background(), cases, "fixture", "candidate", "tenant", "user", searcher, answerer)
	if report.PositiveCaseCount != 1 || report.NoEvidenceCaseCount != 1 {
		t.Fatalf("unexpected case taxonomy: %+v", report)
	}
	if report.Metrics.RecallAt5 != 1 || report.Metrics.CitationCoverage != 1 {
		t.Fatalf("no-evidence cases must not dilute positive retrieval metrics: %+v", report.Metrics)
	}
	if report.Metrics.EvidenceGatePrecision != 1 || report.Metrics.NoEvidenceSafeRate != 1 || report.Metrics.UnsupportedAnswerRate != 0 {
		t.Fatalf("unexpected no-evidence safety metrics: %+v", report.Metrics)
	}
}

func TestRAGDatasetValidationRejectsUnknownEvidence(t *testing.T) {
	dataset := `{"id":"rag-001","type":"rag","difficulty":"easy","question":"q","fixture":"kb-fixture-v1","expected":{"intent":"project_qa","evidence_ids":["missing"]},"reviewed_by":"human","dataset_version":"devsupport-rag-core-v1"}`
	cases, err := LoadRAGCases(strings.NewReader(dataset))
	if err != nil {
		t.Fatal(err)
	}
	fixture := RAGFixture{Version: "kb-fixture-v1", Chunks: []FixtureChunk{{ID: "known", Document: "a.md", Content: "text", LineStart: 1, LineEnd: 1}}}
	if err := ValidateRAGCore(append(cases, make([]RAGCase, RAGCoreCaseCount-1)...), fixture); err == nil {
		t.Fatal("expected invalid dataset")
	}
}

func TestRAGDatasetValidationRejectsSupersededEvidenceLabel(t *testing.T) {
	resolve := true
	cases := make([]RAGCase, RAGCoreCaseCount)
	for index := range cases {
		cases[index] = RAGCase{
			ID: fmt.Sprintf("case-%d", index), Type: "rag", Question: "q", Fixture: "fixture-v2",
			Expected:   RAGExpected{Intent: "project_qa", EvidenceIDs: []string{"active"}, ShouldResolve: &resolve},
			ReviewedBy: "pending_user", DatasetVersion: RAGCoreDatasetVersion,
		}
	}
	cases[0].Expected.EvidenceIDs = []string{"old"}
	fixture := RAGFixture{Version: "fixture-v2", Chunks: []FixtureChunk{
		{ID: "old", Document: "a.md", Content: "old", LineStart: 1, LineEnd: 1, Status: "superseded"},
		{ID: "active", Document: "a.md", Content: "new", LineStart: 1, LineEnd: 1, Status: "active"},
	}}
	if err := ValidateRAGCore(cases, fixture); err == nil || !strings.Contains(err.Error(), "unknown evidence old") {
		t.Fatalf("expected superseded evidence label rejection, got %v", err)
	}
}

func TestFixtureAuthorityEnforcesTenantAndUser(t *testing.T) {
	repository := NewFixtureAuthorityRepository([]rag.ChunkAuthority{{ID: "allowed", TenantID: "tenant", UserID: "user"}, {ID: "other", TenantID: "other", UserID: "user"}})
	chunks, err := repository.FindAccessibleChunks(context.Background(), "tenant", "user", []string{"allowed", "other"})
	if err != nil || len(chunks) != 1 || chunks["allowed"].ID != "allowed" {
		t.Fatalf("ACL was not enforced: chunks=%+v err=%v", chunks, err)
	}
}

func TestVersionedRAGCoreDataset(t *testing.T) {
	datasetFile, err := os.Open("../../evals/devsupport-rag-core-v1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer datasetFile.Close()
	cases, err := LoadRAGCases(datasetFile)
	if err != nil {
		t.Fatal(err)
	}
	fixtureFile, err := os.Open("../../evals/fixtures/kb-fixture-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	defer fixtureFile.Close()
	fixture, err := LoadRAGFixture(fixtureFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRAGCore(cases, fixture); err != nil {
		t.Fatal(err)
	}
}

func TestVersionedRAGCoreV2Dataset(t *testing.T) {
	datasetFile, err := os.Open("../../evals/devsupport-rag-core-v2.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer datasetFile.Close()
	cases, err := LoadRAGCases(datasetFile)
	if err != nil {
		t.Fatal(err)
	}
	fixtureFile, err := os.Open("../../evals/fixtures/kb-fixture-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	defer fixtureFile.Close()
	fixture, err := LoadRAGFixture(fixtureFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRAGCore(cases, fixture); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 60 {
		t.Fatalf("expected 60 v2 cases, got %d", len(cases))
	}
	noEvidence := 0
	for _, item := range cases {
		if !expectsResolution(item) {
			noEvidence++
		}
	}
	if noEvidence != 10 {
		t.Fatalf("expected 10 no-evidence safety cases, got %d", noEvidence)
	}
}
