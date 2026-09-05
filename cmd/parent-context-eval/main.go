package main

import (
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

	redisstore "GopherAI/common/redis"
	"GopherAI/config"
	knowledgeagent "GopherAI/internal/agent/knowledge"
	"GopherAI/internal/evaluation"
	"GopherAI/internal/knowledge"
	"GopherAI/internal/rag"
	"GopherAI/model"

	embeddingArk "github.com/cloudwego/eino-ext/components/embedding/ark"
	modelOpenAI "github.com/cloudwego/eino-ext/components/model/openai"
	rediscli "github.com/redis/go-redis/v9"
)

const (
	evalEnvironment = "eval-parent-context-ab-v1"
	evalTenantID    = "eval-parent-context-tenant-v1"
	evalUserID      = "eval-parent-context-user-v1"
	otherTenantID   = "eval-parent-context-other-tenant-v1"
)

func main() {
	log.SetFlags(0)
	datasetPath := flag.String("dataset", "evals/devsupport-parent-context-ab-v1.jsonl", "versioned parent-context JSONL dataset")
	fixturePath := flag.String("fixture", "evals/fixtures/kb-parent-context-fixture-v1.json", "isolated parent-context fixture")
	jsonPath := flag.String("out-json", "evals/results/devsupport-parent-context-ab-v1-candidate.json", "machine-readable report path")
	markdownPath := flag.String("out-md", "evals/results/devsupport-parent-context-ab-v1-candidate.md", "human-readable report path")
	candidateVersion := flag.String("candidate", "working-tree", "candidate commit or release identifier")
	flag.Parse()
	if err := run(context.Background(), *datasetPath, *fixturePath, *jsonPath, *markdownPath, *candidateVersion); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, datasetPath string, fixturePath string, jsonPath string, markdownPath string, candidateVersion string) error {
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
			log.Printf("warning: isolated parent-context index cleanup failed: %v", cleanupErr)
		}
	}()

	timeout, retryTimes := 45*time.Second, 1
	embedder, err := embeddingArk.NewEmbedder(ctx, &embeddingArk.EmbeddingConfig{
		BaseURL: configuration.RagBaseUrl, APIKey: apiKey, Model: configuration.RagEmbeddingModel,
		Timeout: &timeout, RetryTimes: &retryTimes,
	})
	if err != nil {
		return fmt.Errorf("create parent-context eval embedder: %w", err)
	}
	modelChunks, authorities := fixtureData(fixture)
	indexer, err := knowledge.NewRedisChunkIndexer(redisstore.Rdb, embedder, evalEnvironment, configuration.RagDimension, knowledge.DefaultEmbeddingBatchSize)
	if err != nil {
		return fmt.Errorf("create parent-context eval indexer: %w", err)
	}
	if err := indexer.Index(ctx, modelChunks); err != nil {
		return fmt.Errorf("index parent-context fixture: %w", err)
	}
	time.Sleep(750 * time.Millisecond)

	baseRetriever, err := rag.NewHybridRetriever(rag.NewRedisSearchBackend(redisstore.Rdb), embedder, evaluation.NewFixtureAuthorityRepository(authorities), evalEnvironment, configuration.RagDimension)
	if err != nil {
		return fmt.Errorf("create parent-context eval retriever: %w", err)
	}
	cached := newCachedSearcher(baseRetriever)
	parentRetriever, err := rag.NewParentContextRetriever(cached)
	if err != nil {
		return err
	}
	chatModel, err := modelOpenAI.NewChatModel(ctx, &modelOpenAI.ChatModelConfig{
		BaseURL: configuration.RagBaseUrl, APIKey: apiKey, Model: configuration.RagChatModelName,
	})
	if err != nil {
		return fmt.Errorf("create parent-context eval chat model: %w", err)
	}
	baseline, err := knowledgeagent.NewAgent(cached, chatModel, rag.DefaultEvidenceGate(), rag.NewCitationBuilder())
	if err != nil {
		return err
	}
	candidate, err := knowledgeagent.NewParentContextAgent(parentRetriever, chatModel, rag.DefaultEvidenceGate(), rag.NewCitationBuilder())
	if err != nil {
		return err
	}

	report, err := evaluation.EvaluateParentContextAB(ctx, cases, evalTenantID, evalUserID, candidateVersion, time.Now(), baseline, candidate)
	if err != nil {
		return err
	}
	report.Runtime = evaluation.ParentContextRuntime{
		DatasetSHA256: fileSHA256(datasetPath), FixtureSHA256: fileSHA256(fixturePath),
		BaselineStrategy:  knowledgeagent.StrategyName + "@" + knowledgeagent.StrategyVersion,
		CandidateStrategy: rag.ParentContextStrategyName + "@" + rag.ParentContextStrategyVersion,
		EmbeddingModel:    configuration.RagEmbeddingModel, ChatModel: configuration.RagChatModelName,
		Environment: evalEnvironment, ExternalModelMutable: true,
	}
	if err := writeReports(report, jsonPath, markdownPath); err != nil {
		return err
	}
	encoded, _ := json.Marshal(report.Metrics)
	log.Printf("parent-context paired evaluation technical_gates=%t net_benefit=%t promotion_eligible=%t metrics=%s", report.TechnicalGatesPassed, report.NetBenefitPassed, report.PromotionEligible, encoded)
	if !report.TechnicalGatesPassed {
		return errors.New("parent-context paired evaluation did not meet technical gates; reports were written")
	}
	return nil
}

func loadInputs(datasetPath string, fixturePath string) ([]evaluation.ParentContextCase, evaluation.RAGFixture, error) {
	datasetFile, err := os.Open(datasetPath)
	if err != nil {
		return nil, evaluation.RAGFixture{}, fmt.Errorf("open parent-context dataset: %w", err)
	}
	defer datasetFile.Close()
	cases, err := evaluation.LoadParentContextCases(datasetFile)
	if err != nil {
		return nil, evaluation.RAGFixture{}, err
	}
	fixtureFile, err := os.Open(fixturePath)
	if err != nil {
		return nil, evaluation.RAGFixture{}, fmt.Errorf("open parent-context fixture: %w", err)
	}
	defer fixtureFile.Close()
	fixture, err := evaluation.LoadRAGFixture(fixtureFile)
	if err != nil {
		return nil, evaluation.RAGFixture{}, err
	}
	active := make(map[string]struct{}, len(fixture.Chunks))
	for _, chunk := range fixture.Chunks {
		if chunk.Status != "superseded" {
			active[chunk.ID] = struct{}{}
		}
	}
	for _, item := range cases {
		for _, evidenceID := range item.Expected.EvidenceIDs {
			if _, exists := active[evidenceID]; !exists {
				return nil, evaluation.RAGFixture{}, fmt.Errorf("case %s references inactive fixture evidence %s", item.ID, evidenceID)
			}
		}
	}
	return cases, fixture, nil
}

func fixtureData(fixture evaluation.RAGFixture) ([]model.KnowledgeChunk, []rag.ChunkAuthority) {
	chunks := make([]model.KnowledgeChunk, 0, len(fixture.Chunks)+len(fixture.UnauthorizedChunks))
	authorities := make([]rag.ChunkAuthority, 0, cap(chunks))
	appendChunk := func(item evaluation.FixtureChunk, tenantID string, userID string, ordinal int) {
		documentID := "parent-eval-doc-" + shortHash(item.Document)
		contentHash := fullHash(item.Content)
		documentVersion := item.DocumentVersion
		if documentVersion < 1 {
			documentVersion = 1
		}
		chunks = append(chunks, model.KnowledgeChunk{
			ID: item.ID, DocumentID: documentID, DocumentVersion: documentVersion, TenantID: tenantID, UserID: userID,
			Ordinal: ordinal, SectionPath: item.Section, LineStart: item.LineStart, LineEnd: item.LineEnd,
			Content: item.Content, TokenCount: len([]rune(item.Content)), ContentHash: contentHash,
			ChunkKind: knowledge.ChunkKindChild, ParentChunkID: item.ParentEvidenceID,
			EmbeddingVersion: config.GetConfig().RagEmbeddingModel, IndexStatus: knowledge.ChunkIndexStatusIndexed,
		})
		if item.Status != "superseded" {
			authorities = append(authorities, rag.ChunkAuthority{
				ID: item.ID, DocumentID: documentID, DocumentVersion: documentVersion, TenantID: tenantID, UserID: userID,
				DisplayName: item.Document, SectionPath: item.Section, LineStart: item.LineStart, LineEnd: item.LineEnd,
				Content: item.Content, ContentHash: contentHash, ParentChunkID: item.ParentEvidenceID,
				ParentContext: item.ParentContext, ParentSection: item.ParentSection,
				ParentLineStart: item.ParentLineStart, ParentLineEnd: item.ParentLineEnd,
			})
		}
	}
	for index, item := range fixture.Chunks {
		appendChunk(item, evalTenantID, evalUserID, index)
	}
	for index, item := range fixture.UnauthorizedChunks {
		appendChunk(item, otherTenantID, "eval-parent-context-other-user-v1", index)
	}
	return chunks, authorities
}

func writeReports(report evaluation.ParentContextABReport, jsonPath string, markdownPath string) error {
	for _, path := range []string{jsonPath, markdownPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create parent-context report directory: %w", err)
		}
	}
	jsonFile, err := os.Create(jsonPath)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(jsonFile)
	encoder.SetIndent("", "  ")
	encodeErr, closeErr := encoder.Encode(report), jsonFile.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeErr != nil {
		return closeErr
	}
	markdownFile, err := os.Create(markdownPath)
	if err != nil {
		return err
	}
	writeErr, closeErr := evaluation.WriteParentContextReportMarkdown(markdownFile, report), markdownFile.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
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

func shortHash(value string) string { return fullHash(value)[:16] }

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
