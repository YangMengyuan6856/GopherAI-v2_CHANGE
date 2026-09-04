package evaluation

import (
	knowledgeagent "GopherAI/internal/agent/knowledge"
	"GopherAI/internal/rag"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

const (
	RAGCoreDatasetVersion = "devsupport-rag-core-v1"
	RAGCoreCaseCount      = 20
	RAGCoreCaseTimeout    = 90 * time.Second
)

type RAGExpected struct {
	Intent          string   `json:"intent"`
	EvidenceIDs     []string `json:"evidence_ids"`
	AnswerFacts     []string `json:"answer_facts"`
	ForbiddenClaims []string `json:"forbidden_claims"`
	ShouldUseTool   bool     `json:"should_use_tool"`
	ShouldClarify   bool     `json:"should_clarify"`
}

type RAGCase struct {
	ID             string      `json:"id"`
	Type           string      `json:"type"`
	Difficulty     string      `json:"difficulty"`
	Question       string      `json:"question"`
	History        []string    `json:"history"`
	Fixture        string      `json:"fixture"`
	Expected       RAGExpected `json:"expected"`
	Tags           []string    `json:"tags"`
	ReviewedBy     string      `json:"reviewed_by"`
	DatasetVersion string      `json:"dataset_version"`
}

type FixtureChunk struct {
	ID        string `json:"id"`
	Document  string `json:"document"`
	Section   string `json:"section"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Content   string `json:"content"`
}

type RAGFixture struct {
	Version            string         `json:"version"`
	Chunks             []FixtureChunk `json:"chunks"`
	UnauthorizedChunks []FixtureChunk `json:"unauthorized_chunks"`
}

type RAGSearcher interface {
	Search(ctx context.Context, input rag.SearchInput) (rag.SearchOutput, error)
}

type RAGAnswerer interface {
	Answer(ctx context.Context, input knowledgeagent.Input) (knowledgeagent.Output, error)
}

type RAGProgressObserver func(completed int, total int, result RAGCaseResult)

type RAGCaseResult struct {
	ID                   string   `json:"id"`
	Question             string   `json:"question"`
	ExpectedEvidenceIDs  []string `json:"expected_evidence_ids"`
	RetrievedEvidenceIDs []string `json:"retrieved_evidence_ids,omitempty"`
	CitedEvidenceIDs     []string `json:"cited_evidence_ids,omitempty"`
	RecallAt5            float64  `json:"recall_at_5"`
	NDCGAt5              float64  `json:"ndcg_at_5"`
	ReciprocalRank       float64  `json:"reciprocal_rank"`
	AnswerResolved       bool     `json:"answer_resolved"`
	CitationCovered      bool     `json:"citation_covered"`
	UnauthorizedHits     int      `json:"unauthorized_hits"`
	Error                string   `json:"error,omitempty"`
}

type RAGMetrics struct {
	RecallAt5          float64 `json:"recall_at_5"`
	NDCGAt5            float64 `json:"ndcg_at_5"`
	MRR                float64 `json:"mrr"`
	CitationPrecision  float64 `json:"citation_precision"`
	CitationCoverage   float64 `json:"citation_coverage"`
	UnauthorizedRecall int     `json:"unauthorized_recall"`
	ResolvedAnswerRate float64 `json:"resolved_answer_rate"`
	ErrorRate          float64 `json:"error_rate"`
}

type RAGTargets struct {
	RecallAt5          float64 `json:"recall_at_5"`
	NDCGAt5            float64 `json:"ndcg_at_5"`
	CitationPrecision  float64 `json:"citation_precision"`
	CitationCoverage   float64 `json:"citation_coverage"`
	UnauthorizedRecall int     `json:"unauthorized_recall"`
}

type RAGReport struct {
	SchemaVersion    string          `json:"schema_version"`
	DatasetVersion   string          `json:"dataset_version"`
	FixtureVersion   string          `json:"fixture_version"`
	CandidateVersion string          `json:"candidate_version"`
	Runtime          RAGRuntime      `json:"runtime"`
	HumanReviewed    bool            `json:"human_reviewed"`
	BaselineEligible bool            `json:"baseline_eligible"`
	GeneratedAt      time.Time       `json:"generated_at"`
	CaseCount        int             `json:"case_count"`
	Metrics          RAGMetrics      `json:"metrics"`
	Targets          RAGTargets      `json:"targets"`
	Passed           bool            `json:"passed"`
	Cases            []RAGCaseResult `json:"cases"`
}

type RAGRuntime struct {
	DatasetSHA256        string `json:"dataset_sha256,omitempty"`
	FixtureSHA256        string `json:"fixture_sha256,omitempty"`
	RetrieverVersion     string `json:"retriever_version,omitempty"`
	EmbeddingModel       string `json:"embedding_model,omitempty"`
	ChatModel            string `json:"chat_model,omitempty"`
	Environment          string `json:"environment,omitempty"`
	CaseTimeoutSeconds   int    `json:"case_timeout_seconds"`
	ExternalModelMutable bool   `json:"external_model_mutable"`
}

func DefaultRAGTargets() RAGTargets {
	return RAGTargets{RecallAt5: 0.85, NDCGAt5: 0.75, CitationPrecision: 0.90, CitationCoverage: 0.90, UnauthorizedRecall: 0}
}

func LoadRAGCases(reader io.Reader) ([]RAGCase, error) {
	if reader == nil {
		return nil, errors.New("dataset reader is required")
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	cases := make([]RAGCase, 0, RAGCoreCaseCount)
	seen := make(map[string]struct{}, RAGCoreCaseCount)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		item := RAGCase{}
		if err := json.Unmarshal([]byte(text), &item); err != nil {
			return nil, fmt.Errorf("decode dataset line %d: %w", line, err)
		}
		if err := validateRAGCase(item); err != nil {
			return nil, fmt.Errorf("dataset line %d: %w", line, err)
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return nil, fmt.Errorf("dataset line %d: duplicate case id %s", line, item.ID)
		}
		seen[item.ID] = struct{}{}
		cases = append(cases, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}
	return cases, nil
}

func LoadRAGFixture(reader io.Reader) (RAGFixture, error) {
	if reader == nil {
		return RAGFixture{}, errors.New("fixture reader is required")
	}
	fixture := RAGFixture{}
	if err := json.NewDecoder(reader).Decode(&fixture); err != nil {
		return RAGFixture{}, fmt.Errorf("decode fixture: %w", err)
	}
	if strings.TrimSpace(fixture.Version) == "" || len(fixture.Chunks) == 0 {
		return RAGFixture{}, errors.New("fixture version and chunks are required")
	}
	seen := make(map[string]struct{}, len(fixture.Chunks)+len(fixture.UnauthorizedChunks))
	for _, chunk := range append(append([]FixtureChunk{}, fixture.Chunks...), fixture.UnauthorizedChunks...) {
		if strings.TrimSpace(chunk.ID) == "" || strings.TrimSpace(chunk.Document) == "" || strings.TrimSpace(chunk.Content) == "" || chunk.LineStart < 1 || chunk.LineEnd < chunk.LineStart {
			return RAGFixture{}, fmt.Errorf("invalid fixture chunk %q", chunk.ID)
		}
		if _, duplicate := seen[chunk.ID]; duplicate {
			return RAGFixture{}, fmt.Errorf("duplicate fixture chunk id %s", chunk.ID)
		}
		seen[chunk.ID] = struct{}{}
	}
	return fixture, nil
}

func ValidateRAGCore(cases []RAGCase, fixture RAGFixture) error {
	if len(cases) != RAGCoreCaseCount {
		return fmt.Errorf("core dataset must contain %d cases, got %d", RAGCoreCaseCount, len(cases))
	}
	known := make(map[string]struct{}, len(fixture.Chunks))
	for _, chunk := range fixture.Chunks {
		known[chunk.ID] = struct{}{}
	}
	for _, item := range cases {
		if item.Fixture != fixture.Version {
			return fmt.Errorf("case %s requires fixture %s, got %s", item.ID, item.Fixture, fixture.Version)
		}
		for _, evidenceID := range item.Expected.EvidenceIDs {
			if _, exists := known[evidenceID]; !exists {
				return fmt.Errorf("case %s references unknown evidence %s", item.ID, evidenceID)
			}
		}
	}
	return nil
}

func RunRAGCore(ctx context.Context, cases []RAGCase, fixtureVersion string, candidateVersion string, tenantID string, userID string, searcher RAGSearcher, answerer RAGAnswerer) RAGReport {
	return RunRAGCoreWithObserver(ctx, cases, fixtureVersion, candidateVersion, tenantID, userID, searcher, answerer, nil)
}

func RunRAGCoreWithObserver(ctx context.Context, cases []RAGCase, fixtureVersion string, candidateVersion string, tenantID string, userID string, searcher RAGSearcher, answerer RAGAnswerer, observer RAGProgressObserver) RAGReport {
	targets := DefaultRAGTargets()
	report := RAGReport{
		SchemaVersion: "1", DatasetVersion: RAGCoreDatasetVersion, FixtureVersion: fixtureVersion,
		CandidateVersion: strings.TrimSpace(candidateVersion), GeneratedAt: time.Now().UTC(), CaseCount: len(cases), Targets: targets,
		Cases: make([]RAGCaseResult, 0, len(cases)),
	}
	report.HumanReviewed = true
	for _, item := range cases {
		if item.ReviewedBy != "human" {
			report.HumanReviewed = false
			break
		}
	}
	if searcher == nil || answerer == nil || len(cases) == 0 {
		return report
	}

	var recallSum, ndcgSum, mrrSum float64
	var relevantCitations, totalCitations, coveredCases, resolvedCases, errorCases, unauthorized int
	for _, item := range cases {
		caseContext, cancelCase := context.WithTimeout(ctx, RAGCoreCaseTimeout)
		result := RAGCaseResult{ID: item.ID, Question: item.Question, ExpectedEvidenceIDs: append([]string(nil), item.Expected.EvidenceIDs...)}
		searchOutput, searchErr := searcher.Search(caseContext, rag.SearchInput{TenantID: tenantID, UserID: userID, Query: item.Question, TopK: 5})
		if searchErr != nil {
			result.Error = "search: " + searchErr.Error()
			errorCases++
			report.Cases = append(report.Cases, result)
			notifyRAGProgress(observer, len(report.Cases), len(cases), result)
			cancelCase()
			continue
		}
		for _, hit := range searchOutput.Hits {
			result.RetrievedEvidenceIDs = append(result.RetrievedEvidenceIDs, hit.Evidence.ID)
			if hit.Evidence.TenantID != tenantID || hit.Evidence.TenantID == "" || hit.Evidence.SourceID == "" {
				result.UnauthorizedHits++
			}
		}
		result.RecallAt5, result.NDCGAt5, result.ReciprocalRank = retrievalScores(result.RetrievedEvidenceIDs, item.Expected.EvidenceIDs)
		recallSum += result.RecallAt5
		ndcgSum += result.NDCGAt5
		mrrSum += result.ReciprocalRank
		unauthorized += result.UnauthorizedHits

		answerOutput, answerErr := answerer.Answer(caseContext, knowledgeagent.Input{TenantID: tenantID, UserID: userID, Question: item.Question, TopK: 5})
		if answerErr != nil {
			result.Error = "answer: " + answerErr.Error()
			errorCases++
			report.Cases = append(report.Cases, result)
			notifyRAGProgress(observer, len(report.Cases), len(cases), result)
			cancelCase()
			continue
		}
		result.AnswerResolved = answerOutput.Result.Resolved
		if result.AnswerResolved {
			resolvedCases++
		}
		for _, citation := range answerOutput.Result.Citations {
			result.CitedEvidenceIDs = append(result.CitedEvidenceIDs, citation.EvidenceID)
			// Unresolved answers deliberately expose the authorized evidence that
			// was inspected while making no factual claim. Keep those citations in
			// the case trace, but do not score them as answer citations. Safety
			// fallback quality is already reflected by coverage/resolved rate.
			if result.AnswerResolved {
				totalCitations++
				if contains(item.Expected.EvidenceIDs, citation.EvidenceID) {
					relevantCitations++
				}
			}
		}
		result.CitationCovered = result.AnswerResolved && containsAll(result.CitedEvidenceIDs, item.Expected.EvidenceIDs)
		if result.CitationCovered {
			coveredCases++
		}
		report.Cases = append(report.Cases, result)
		notifyRAGProgress(observer, len(report.Cases), len(cases), result)
		cancelCase()
	}

	caseCount := float64(len(cases))
	report.Metrics = RAGMetrics{
		RecallAt5: recallSum / caseCount, NDCGAt5: ndcgSum / caseCount, MRR: mrrSum / caseCount,
		CitationPrecision: safeRatio(relevantCitations, totalCitations), CitationCoverage: float64(coveredCases) / caseCount,
		UnauthorizedRecall: unauthorized, ResolvedAnswerRate: float64(resolvedCases) / caseCount, ErrorRate: float64(errorCases) / caseCount,
	}
	report.Passed = report.Metrics.RecallAt5 >= targets.RecallAt5 && report.Metrics.NDCGAt5 >= targets.NDCGAt5 &&
		report.Metrics.CitationPrecision >= targets.CitationPrecision && report.Metrics.CitationCoverage >= targets.CitationCoverage &&
		report.Metrics.UnauthorizedRecall == targets.UnauthorizedRecall && errorCases == 0
	report.BaselineEligible = report.Passed && report.HumanReviewed
	return report
}

func notifyRAGProgress(observer RAGProgressObserver, completed int, total int, result RAGCaseResult) {
	if observer != nil {
		observer(completed, total, result)
	}
}

func WriteRAGReportMarkdown(writer io.Writer, report RAGReport) error {
	if writer == nil {
		return errors.New("report writer is required")
	}
	status := "FAIL"
	if report.Passed {
		status = "PASS"
	}
	_, err := fmt.Fprintf(writer, `# DevSupport RAG Core Evaluation

- Technical metric status: **%s**
- Dataset: %s (%d cases)
- Fixture: %s
- Candidate: %s
- Generated at: %s
- Dataset SHA-256: %s
- Fixture SHA-256: %s
- Retriever: %s
- Embedding model: %s
- Chat model: %s
- Environment: %s
- Per-case timeout: %ds
- External model behavior mutable: %t
- Human label review complete: %t
- Eligible to freeze as baseline: %t

| Metric | Actual | Target |
|---|---:|---:|
| Recall@5 | %.4f | >= %.2f |
| nDCG@5 | %.4f | >= %.2f |
| MRR | %.4f | report |
| Citation Precision | %.4f | >= %.2f |
| Citation Coverage | %.4f | >= %.2f |
| Unauthorized Recall | %d | = %d |
| Resolved Answer Rate | %.4f | report |
| Error Rate | %.4f | = 0 |

Citation Coverage in this M3 core slice is a conservative evidence-reference proxy: a case passes only when the answer is resolved and every human-labelled relevant chunk is cited. Claim-level semantic coverage and LLM-as-a-Judge remain M8 scope.

| Case | Recall@5 | nDCG@5 | RR | Resolved | Citation covered | Error |
|---|---:|---:|---:|---|---|---|
`, status, report.DatasetVersion, report.CaseCount, report.FixtureVersion, report.CandidateVersion, report.GeneratedAt.Format(time.RFC3339),
		report.Runtime.DatasetSHA256, report.Runtime.FixtureSHA256, report.Runtime.RetrieverVersion, report.Runtime.EmbeddingModel,
		report.Runtime.ChatModel, report.Runtime.Environment, report.Runtime.CaseTimeoutSeconds, report.Runtime.ExternalModelMutable,
		report.HumanReviewed, report.BaselineEligible,
		report.Metrics.RecallAt5, report.Targets.RecallAt5, report.Metrics.NDCGAt5, report.Targets.NDCGAt5, report.Metrics.MRR,
		report.Metrics.CitationPrecision, report.Targets.CitationPrecision, report.Metrics.CitationCoverage, report.Targets.CitationCoverage,
		report.Metrics.UnauthorizedRecall, report.Targets.UnauthorizedRecall, report.Metrics.ResolvedAnswerRate, report.Metrics.ErrorRate)
	if err != nil {
		return err
	}
	for _, item := range report.Cases {
		errorText := strings.ReplaceAll(item.Error, "|", "\\|")
		if _, err := fmt.Fprintf(writer, "| %s | %.4f | %.4f | %.4f | %t | %t | %s |\n", item.ID, item.RecallAt5, item.NDCGAt5, item.ReciprocalRank, item.AnswerResolved, item.CitationCovered, errorText); err != nil {
			return err
		}
	}
	return nil
}

func retrievalScores(actual []string, relevant []string) (float64, float64, float64) {
	if len(relevant) == 0 {
		return 0, 0, 0
	}
	relevantSet := make(map[string]struct{}, len(relevant))
	for _, id := range relevant {
		relevantSet[id] = struct{}{}
	}
	limit := min(5, len(actual))
	found := make(map[string]struct{}, len(relevant))
	dcg := 0.0
	reciprocalRank := 0.0
	for index := 0; index < limit; index++ {
		if _, match := relevantSet[actual[index]]; !match {
			continue
		}
		found[actual[index]] = struct{}{}
		dcg += 1 / math.Log2(float64(index+2))
		if reciprocalRank == 0 {
			reciprocalRank = 1 / float64(index+1)
		}
	}
	idealCount := min(5, len(relevant))
	idcg := 0.0
	for index := 0; index < idealCount; index++ {
		idcg += 1 / math.Log2(float64(index+2))
	}
	return float64(len(found)) / float64(len(relevant)), dcg / idcg, reciprocalRank
}

func validateRAGCase(item RAGCase) error {
	if strings.TrimSpace(item.ID) == "" || item.Type != "rag" || strings.TrimSpace(item.Question) == "" || strings.TrimSpace(item.Fixture) == "" || len(item.Expected.EvidenceIDs) == 0 {
		return errors.New("id, rag type, question, fixture and expected evidence are required")
	}
	if item.Expected.Intent != "project_qa" || item.DatasetVersion != RAGCoreDatasetVersion || (item.ReviewedBy != "human" && item.ReviewedBy != "pending_user") {
		return errors.New("core case must be project_qa with the current dataset version and an explicit review state")
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsAll(actual []string, expected []string) bool {
	if len(expected) == 0 {
		return false
	}
	for _, value := range expected {
		if !contains(actual, value) {
			return false
		}
	}
	return true
}

func safeRatio(numerator int, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

type FixtureAuthorityRepository struct {
	chunks map[string]rag.ChunkAuthority
}

func NewFixtureAuthorityRepository(chunks []rag.ChunkAuthority) *FixtureAuthorityRepository {
	byID := make(map[string]rag.ChunkAuthority, len(chunks))
	for _, chunk := range chunks {
		byID[chunk.ID] = chunk
	}
	return &FixtureAuthorityRepository{chunks: byID}
}

func (repository *FixtureAuthorityRepository) FindAccessibleChunks(_ context.Context, tenantID string, userID string, chunkIDs []string) (map[string]rag.ChunkAuthority, error) {
	result := make(map[string]rag.ChunkAuthority, len(chunkIDs))
	for _, id := range chunkIDs {
		chunk, exists := repository.chunks[id]
		if exists && chunk.TenantID == tenantID && chunk.UserID == userID {
			result[id] = chunk
		}
	}
	return result, nil
}

var _ rag.AuthorityRepository = (*FixtureAuthorityRepository)(nil)
