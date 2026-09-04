package evaluation

import (
	knowledgeagent "GopherAI/internal/agent/knowledge"
	"GopherAI/internal/contract"
	"GopherAI/internal/rag"
	"context"
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
		"q1": {Result: contract.AgentResult{Resolved: true, Citations: []contract.Citation{{EvidenceID: "a"}}}},
		"q2": {Result: contract.AgentResult{Resolved: true, Citations: []contract.Citation{{EvidenceID: "b"}, {EvidenceID: "c"}}}},
	}}
	report := RunRAGCore(context.Background(), cases, "fixture", "candidate", "tenant", "user", searcher, answerer)
	if report.Metrics.RecallAt5 != 1 || report.Metrics.CitationPrecision != 1 || report.Metrics.CitationCoverage != 1 || report.Metrics.UnauthorizedRecall != 0 {
		t.Fatalf("unexpected metrics: %+v", report.Metrics)
	}
	if report.Metrics.NDCGAt5 >= 1 || report.Metrics.MRR >= 1 {
		t.Fatalf("ranking penalty was not applied: %+v", report.Metrics)
	}
	var markdown strings.Builder
	if err := WriteRAGReportMarkdown(&markdown, report); err != nil || !strings.Contains(markdown.String(), "Citation Precision") {
		t.Fatalf("unexpected markdown report: %v %s", err, markdown.String())
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
