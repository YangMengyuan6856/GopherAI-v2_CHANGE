package main

import (
	redisstore "GopherAI/common/redis"
	"GopherAI/config"
	knowledgeagent "GopherAI/internal/agent/knowledge"
	"GopherAI/internal/evaluation"
	"GopherAI/internal/knowledge"
	"GopherAI/internal/rag"
	"GopherAI/model"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	embeddingArk "github.com/cloudwego/eino-ext/components/embedding/ark"
	modelOpenAI "github.com/cloudwego/eino-ext/components/model/openai"
	rediscli "github.com/redis/go-redis/v9"
)

const (
	evalEnvironment = "eval-rag-core-v2"
	evalTenantID    = "eval-tenant-v2"
	evalUserID      = "eval-user-v2"
	otherTenantID   = "eval-other-tenant-v2"
)

func main() {
	log.SetFlags(0)
	datasetPath := flag.String("dataset", "evals/devsupport-rag-core-v2.jsonl", "versioned RAG JSONL dataset")
	fixturePath := flag.String("fixture", "evals/fixtures/kb-fixture-v2.json", "isolated RAG fixture")
	jsonPath := flag.String("out-json", "evals/reports/devsupport-rag-core-latest.json", "machine-readable report path")
	markdownPath := flag.String("out-md", "evals/reports/devsupport-rag-core-latest.md", "human-readable report path")
	candidate := flag.String("candidate", "working-tree", "candidate commit or release identifier")
	flag.Parse()

	if err := run(context.Background(), *datasetPath, *fixturePath, *jsonPath, *markdownPath, *candidate); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, datasetPath string, fixturePath string, jsonPath string, markdownPath string, candidate string) error {
	cases, fixture, err := loadInputs(datasetPath, fixturePath)
	if err != nil {
		return err
	}
	configuration := config.GetConfig()
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return errors.New("OPENAI_API_KEY is required")
	}
	redisstore.Init()
	if redisstore.Rdb == nil {
		return errors.New("redis client is unavailable")
	}
	if err := redisstore.Rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}
	if err := dropEvalIndex(ctx); err != nil {
		return err
	}
	defer func() {
		if cleanupErr := dropEvalIndex(context.Background()); cleanupErr != nil {
			log.Printf("warning: isolated eval index cleanup failed: %v", cleanupErr)
		}
	}()

	timeout := 45 * time.Second
	retryTimes := 1
	embedder, err := embeddingArk.NewEmbedder(ctx, &embeddingArk.EmbeddingConfig{
		BaseURL: configuration.RagBaseUrl, APIKey: apiKey, Model: configuration.RagEmbeddingModel,
		Timeout: &timeout, RetryTimes: &retryTimes,
	})
	if err != nil {
		return fmt.Errorf("create eval embedder: %w", err)
	}
	modelChunks, authorities := fixtureData(fixture)
	indexer, err := knowledge.NewRedisChunkIndexer(redisstore.Rdb, embedder, evalEnvironment, configuration.RagDimension, knowledge.DefaultEmbeddingBatchSize)
	if err != nil {
		return fmt.Errorf("create isolated indexer: %w", err)
	}
	if err := indexer.Index(ctx, modelChunks); err != nil {
		return fmt.Errorf("index isolated fixture: %w", err)
	}
	time.Sleep(750 * time.Millisecond)

	retriever, err := rag.NewHybridRetriever(
		rag.NewRedisSearchBackend(redisstore.Rdb), embedder,
		evaluation.NewFixtureAuthorityRepository(authorities), evalEnvironment, configuration.RagDimension,
	)
	if err != nil {
		return fmt.Errorf("create eval retriever: %w", err)
	}
	chatModel, err := modelOpenAI.NewChatModel(ctx, &modelOpenAI.ChatModelConfig{
		BaseURL: configuration.RagBaseUrl, APIKey: apiKey, Model: configuration.RagChatModelName,
	})
	if err != nil {
		return fmt.Errorf("create eval chat model: %w", err)
	}
	cachedRetriever := newCachedSearcher(retriever)
	answerer, err := knowledgeagent.NewAgent(cachedRetriever, chatModel, rag.DefaultEvidenceGate(), rag.NewCitationBuilder())
	if err != nil {
		return fmt.Errorf("create eval knowledge agent: %w", err)
	}

	report := evaluation.RunRAGCoreWithObserver(ctx, cases, fixture.Version, candidate, evalTenantID, evalUserID, cachedRetriever, answerer,
		func(completed int, total int, result evaluation.RAGCaseResult) {
			log.Printf("rag eval progress=%d/%d case=%s recall_at_5=%.4f ndcg_at_5=%.4f resolved=%t citation_covered=%t error=%t",
				completed, total, result.ID, result.RecallAt5, result.NDCGAt5, result.AnswerResolved, result.CitationCovered, result.Error != "")
		})
	report.Runtime = evaluation.RAGRuntime{
		DatasetSHA256: fileSHA256(datasetPath), FixtureSHA256: fileSHA256(fixturePath), RetrieverVersion: rag.RetrievalVersion,
		EmbeddingModel: configuration.RagEmbeddingModel, ChatModel: configuration.RagChatModelName, Environment: evalEnvironment,
		CaseTimeoutSeconds: int(evaluation.RAGCoreCaseTimeout.Seconds()), ExternalModelMutable: true,
	}
	if err := writeReports(report, jsonPath, markdownPath); err != nil {
		return err
	}
	encoded, _ := json.Marshal(report.Metrics)
	log.Printf("rag core evaluation passed=%t metrics=%s", report.Passed, encoded)
	if !report.Passed {
		return errors.New("RAG core evaluation did not meet its release targets; reports were written")
	}
	return nil
}

func loadInputs(datasetPath string, fixturePath string) ([]evaluation.RAGCase, evaluation.RAGFixture, error) {
	datasetFile, err := os.Open(datasetPath)
	if err != nil {
		return nil, evaluation.RAGFixture{}, fmt.Errorf("open dataset: %w", err)
	}
	defer datasetFile.Close()
	cases, err := evaluation.LoadRAGCases(datasetFile)
	if err != nil {
		return nil, evaluation.RAGFixture{}, err
	}
	fixtureFile, err := os.Open(fixturePath)
	if err != nil {
		return nil, evaluation.RAGFixture{}, fmt.Errorf("open fixture: %w", err)
	}
	defer fixtureFile.Close()
	fixture, err := evaluation.LoadRAGFixture(fixtureFile)
	if err != nil {
		return nil, evaluation.RAGFixture{}, err
	}
	if err := evaluation.ValidateRAGCore(cases, fixture); err != nil {
		return nil, evaluation.RAGFixture{}, err
	}
	return cases, fixture, nil
}

func fixtureData(fixture evaluation.RAGFixture) ([]model.KnowledgeChunk, []rag.ChunkAuthority) {
	chunks := make([]model.KnowledgeChunk, 0, len(fixture.Chunks)+len(fixture.UnauthorizedChunks))
	authorities := make([]rag.ChunkAuthority, 0, cap(chunks))
	appendChunk := func(item evaluation.FixtureChunk, tenantID string, userID string, ordinal int) {
		documentID := "eval-doc-" + shortHash(item.Document)
		contentHash := fullHash(item.Content)
		documentVersion := item.DocumentVersion
		if documentVersion < 1 {
			documentVersion = 1
		}
		chunks = append(chunks, model.KnowledgeChunk{
			ID: item.ID, DocumentID: documentID, DocumentVersion: documentVersion, TenantID: tenantID, UserID: userID,
			Ordinal: ordinal, SectionPath: item.Section, LineStart: item.LineStart, LineEnd: item.LineEnd,
			Content: item.Content, TokenCount: len([]rune(item.Content)), ContentHash: contentHash,
			EmbeddingVersion: config.GetConfig().RagEmbeddingModel, IndexStatus: knowledge.ChunkIndexStatusIndexed,
		})
		if item.Status != "superseded" {
			authorities = append(authorities, rag.ChunkAuthority{
				ID: item.ID, DocumentID: documentID, DocumentVersion: documentVersion, TenantID: tenantID, UserID: userID,
				DisplayName: item.Document, SectionPath: item.Section, LineStart: item.LineStart, LineEnd: item.LineEnd,
				Content: item.Content, ContentHash: contentHash,
			})
		}
	}
	for index, item := range fixture.Chunks {
		appendChunk(item, evalTenantID, evalUserID, index)
	}
	for index, item := range fixture.UnauthorizedChunks {
		appendChunk(item, otherTenantID, "eval-other-user-v1", index)
	}
	return chunks, authorities
}

func writeReports(report evaluation.RAGReport, jsonPath string, markdownPath string) error {
	for _, path := range []string{jsonPath, markdownPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create report directory: %w", err)
		}
	}
	jsonFile, err := os.Create(jsonPath)
	if err != nil {
		return fmt.Errorf("create JSON report: %w", err)
	}
	encoder := json.NewEncoder(jsonFile)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(report)
	closeErr := jsonFile.Close()
	if encodeErr != nil {
		return fmt.Errorf("write JSON report: %w", encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close JSON report: %w", closeErr)
	}
	markdownFile, err := os.Create(markdownPath)
	if err != nil {
		return fmt.Errorf("create Markdown report: %w", err)
	}
	writeErr := evaluation.WriteRAGReportMarkdown(markdownFile, report)
	closeErr = markdownFile.Close()
	if writeErr != nil {
		return fmt.Errorf("write Markdown report: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close Markdown report: %w", closeErr)
	}
	return nil
}

func dropEvalIndex(ctx context.Context) error {
	indexName := "gopher:" + evalEnvironment + ":v1:kb:chunks:idx"
	err := redisstore.Rdb.Do(ctx, "FT.DROPINDEX", indexName, "DD").Err()
	if err == nil || errors.Is(err, rediscli.Nil) || strings.Contains(strings.ToLower(err.Error()), "unknown index") {
		return nil
	}
	return fmt.Errorf("drop isolated eval index %s: %w", indexName, err)
}

func fullHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func shortHash(value string) string {
	return fullHash(value)[:16]
}

func fileSHA256(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return "unavailable"
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

type cachedSearcher struct {
	inner evaluation.RAGSearcher
	mu    sync.Mutex
	cache map[string]rag.SearchOutput
}

func newCachedSearcher(inner evaluation.RAGSearcher) *cachedSearcher {
	return &cachedSearcher{inner: inner, cache: make(map[string]rag.SearchOutput)}
}

func (searcher *cachedSearcher) Search(ctx context.Context, input rag.SearchInput) (rag.SearchOutput, error) {
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%s", input.TenantID, input.UserID, input.TopK, input.Query)
	searcher.mu.Lock()
	cached, exists := searcher.cache[key]
	searcher.mu.Unlock()
	if exists {
		return cached, nil
	}
	output, err := searcher.inner.Search(ctx, input)
	if err != nil {
		return rag.SearchOutput{}, err
	}
	searcher.mu.Lock()
	searcher.cache[key] = output
	searcher.mu.Unlock()
	return output, nil
}

var _ evaluation.RAGSearcher = (*cachedSearcher)(nil)
