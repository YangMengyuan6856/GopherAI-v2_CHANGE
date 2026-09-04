package rag

import (
	"GopherAI/internal/contract"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/redis/go-redis/v9"
)

const (
	DefaultTopK        = 5
	MaxTopK            = 10
	CandidateTopK      = 20
	ReciprocalRankK    = 60
	RetrievalVersion   = "hybrid-rrf-v1"
	defaultEnvironment = "prod"
)

var (
	ErrInvalidSearch        = errors.New("invalid knowledge search")
	ErrRetrievalUnavailable = errors.New("all knowledge retrievers are unavailable")
	unsafeEnvironment       = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
)

type SearchBackend interface {
	Search(ctx context.Context, index string, query string, options *redis.FTSearchOptions) (redis.FTSearchResult, error)
}

type RedisSearchBackend struct {
	client *redis.Client
}

func NewRedisSearchBackend(client *redis.Client) *RedisSearchBackend {
	return &RedisSearchBackend{client: client}
}

func (backend *RedisSearchBackend) Search(ctx context.Context, index string, query string, options *redis.FTSearchOptions) (redis.FTSearchResult, error) {
	if backend == nil || backend.client == nil {
		return redis.FTSearchResult{}, errors.New("redis search client is unavailable")
	}
	return backend.client.FTSearchWithArgs(ctx, index, query, options).Result()
}

type ChunkAuthority struct {
	ID              string
	DocumentID      string
	DocumentVersion int
	TenantID        string
	UserID          string
	DisplayName     string
	SectionPath     string
	LineStart       int
	LineEnd         int
	Content         string
	ContentHash     string
}

type AuthorityRepository interface {
	FindAccessibleChunks(ctx context.Context, tenantID string, userID string, chunkIDs []string) (map[string]ChunkAuthority, error)
}

type SearchInput struct {
	TenantID string
	UserID   string
	Query    string
	TopK     int
}

type SearchHit struct {
	Evidence    contract.Evidence `json:"evidence"`
	DenseRank   int               `json:"dense_rank,omitempty"`
	KeywordRank int               `json:"keyword_rank,omitempty"`
	RRFScore    float64           `json:"rrf_score"`
}

type SearchDiagnostics struct {
	Version           string   `json:"version"`
	Mode              string   `json:"mode"`
	DenseCandidates   int      `json:"dense_candidates"`
	KeywordCandidates int      `json:"keyword_candidates"`
	FusedCandidates   int      `json:"fused_candidates"`
	DegradedReasons   []string `json:"degraded_reasons,omitempty"`
}

type SearchOutput struct {
	Hits        []SearchHit       `json:"hits"`
	Diagnostics SearchDiagnostics `json:"diagnostics"`
}

type HybridRetriever struct {
	backend   SearchBackend
	embedder  embedding.Embedder
	authority AuthorityRepository
	indexName string
	keyPrefix string
	dimension int
}

type rankedCandidate struct {
	chunkID     string
	denseRank   int
	keywordRank int
	rrfScore    float64
}

func NewHybridRetriever(backend SearchBackend, embedder embedding.Embedder, authority AuthorityRepository, environment string, dimension int) (*HybridRetriever, error) {
	if backend == nil || embedder == nil || authority == nil || dimension <= 0 {
		return nil, errors.New("search backend, embedder, authority repository and positive dimension are required")
	}
	environment = unsafeEnvironment.ReplaceAllString(strings.TrimSpace(environment), "-")
	if environment == "" {
		environment = defaultEnvironment
	}
	base := fmt.Sprintf("gopher:%s:v1:kb", environment)
	return &HybridRetriever{
		backend: backend, embedder: embedder, authority: authority,
		indexName: base + ":chunks:idx", keyPrefix: base + ":chunk:", dimension: dimension,
	}, nil
}

func (retriever *HybridRetriever) Search(ctx context.Context, input SearchInput) (SearchOutput, error) {
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.UserID = strings.TrimSpace(input.UserID)
	input.Query = strings.TrimSpace(input.Query)
	if input.TenantID == "" || input.UserID == "" || input.Query == "" || len([]rune(input.Query)) > 2000 {
		return SearchOutput{}, ErrInvalidSearch
	}
	if input.TopK == 0 {
		input.TopK = DefaultTopK
	}
	if input.TopK < 1 || input.TopK > MaxTopK {
		return SearchOutput{}, ErrInvalidSearch
	}

	dense, denseErr := retriever.dense(ctx, input)
	keyword, keywordErr := retriever.keyword(ctx, input)
	diagnostics := SearchDiagnostics{
		Version: RetrievalVersion, DenseCandidates: len(dense), KeywordCandidates: len(keyword),
	}
	switch {
	case denseErr == nil && keywordErr == nil:
		diagnostics.Mode = "hybrid"
	case denseErr == nil:
		diagnostics.Mode = "dense_only"
		diagnostics.DegradedReasons = []string{"keyword_unavailable"}
	case keywordErr == nil:
		diagnostics.Mode = "bm25_only"
		diagnostics.DegradedReasons = []string{"dense_unavailable"}
	default:
		return SearchOutput{Diagnostics: diagnostics}, fmt.Errorf("%w: dense and keyword search failed", ErrRetrievalUnavailable)
	}

	fused := reciprocalRankFusion(dense, keyword)
	if len(fused) == 0 {
		return SearchOutput{Hits: []SearchHit{}, Diagnostics: diagnostics}, nil
	}
	chunkIDs := make([]string, 0, len(fused))
	for _, candidate := range fused {
		chunkIDs = append(chunkIDs, candidate.chunkID)
	}
	authority, err := retriever.authority.FindAccessibleChunks(ctx, input.TenantID, input.UserID, chunkIDs)
	if err != nil {
		return SearchOutput{Diagnostics: diagnostics}, fmt.Errorf("load authoritative chunks: %w", err)
	}

	hits := make([]SearchHit, 0, min(input.TopK, len(fused)))
	seenHashes := make(map[string]struct{}, len(fused))
	for _, candidate := range fused {
		chunk, exists := authority[candidate.chunkID]
		if !exists || chunk.TenantID != input.TenantID || chunk.UserID != input.UserID {
			continue
		}
		dedupeKey := chunk.ContentHash
		if dedupeKey == "" {
			dedupeKey = chunk.ID
		}
		if _, duplicate := seenHashes[dedupeKey]; duplicate {
			continue
		}
		seenHashes[dedupeKey] = struct{}{}
		retrieval := candidateRetrieval(candidate)
		hits = append(hits, SearchHit{
			Evidence: contract.Evidence{
				ID: chunk.ID, Kind: "document_chunk", TenantID: chunk.TenantID,
				SourceID: chunk.DocumentID, SourceVersion: strconv.Itoa(chunk.DocumentVersion),
				Title: chunk.DisplayName, Section: chunk.SectionPath, LineStart: chunk.LineStart, LineEnd: chunk.LineEnd,
				Content: chunk.Content, Score: normalizedRRF(candidate.rrfScore), Retrieval: retrieval, ContentHash: chunk.ContentHash,
			},
			DenseRank: candidate.denseRank, KeywordRank: candidate.keywordRank, RRFScore: candidate.rrfScore,
		})
		if len(hits) == input.TopK {
			break
		}
	}
	diagnostics.FusedCandidates = len(hits)
	return SearchOutput{Hits: hits, Diagnostics: diagnostics}, nil
}

func (retriever *HybridRetriever) dense(ctx context.Context, input SearchInput) ([]rankedCandidate, error) {
	vectors, err := retriever.embedder.EmbedStrings(ctx, []string{input.Query})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 || len(vectors[0]) != retriever.dimension {
		return nil, fmt.Errorf("query embedding dimension mismatch")
	}
	filter := aclFilter(input.TenantID, input.UserID)
	query := fmt.Sprintf("(%s)=>[KNN %d @vector $query_vector AS vector_distance]", filter, CandidateTopK)
	options := &redis.FTSearchOptions{
		Return: []redis.FTSearchReturn{
			{FieldName: "content_hash"}, {FieldName: "vector_distance"},
		},
		SortBy:      []redis.FTSearchSortBy{{FieldName: "vector_distance", Asc: true}},
		LimitOffset: 0, Limit: CandidateTopK, DialectVersion: 2,
		Params: map[string]interface{}{"query_vector": float32Vector(vectors[0])},
	}
	result, err := retriever.backend.Search(ctx, retriever.indexName, query, options)
	if err != nil {
		return nil, err
	}
	return redisCandidates(result, retriever.keyPrefix, true), nil
}

func (retriever *HybridRetriever) keyword(ctx context.Context, input SearchInput) ([]rankedCandidate, error) {
	terms := keywordTerms(input.Query)
	if len(terms) == 0 {
		return []rankedCandidate{}, nil
	}
	termQuery := strings.Join(terms, " | ")
	query := fmt.Sprintf("(%s) (@content:(%s) | @section_path:(%s))", aclFilter(input.TenantID, input.UserID), termQuery, termQuery)
	options := &redis.FTSearchOptions{
		Return:     []redis.FTSearchReturn{{FieldName: "content_hash"}},
		WithScores: true, Scorer: "BM25", LimitOffset: 0, Limit: CandidateTopK, DialectVersion: 2,
	}
	result, err := retriever.backend.Search(ctx, retriever.indexName, query, options)
	if err != nil {
		return nil, err
	}
	return redisCandidates(result, retriever.keyPrefix, false), nil
}

func redisCandidates(result redis.FTSearchResult, keyPrefix string, dense bool) []rankedCandidate {
	candidates := make([]rankedCandidate, 0, len(result.Docs))
	for _, document := range result.Docs {
		if document.Error != nil {
			continue
		}
		chunkID := strings.TrimPrefix(document.ID, keyPrefix)
		if chunkID == "" || chunkID == document.ID {
			continue
		}
		candidate := rankedCandidate{chunkID: chunkID}
		if dense {
			candidate.denseRank = len(candidates) + 1
		} else {
			candidate.keywordRank = len(candidates) + 1
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func reciprocalRankFusion(dense []rankedCandidate, keyword []rankedCandidate) []rankedCandidate {
	byID := make(map[string]*rankedCandidate, len(dense)+len(keyword))
	for _, list := range [][]rankedCandidate{dense, keyword} {
		for _, item := range list {
			candidate := byID[item.chunkID]
			if candidate == nil {
				candidate = &rankedCandidate{chunkID: item.chunkID}
				byID[item.chunkID] = candidate
			}
			if item.denseRank > 0 {
				candidate.denseRank = item.denseRank
				candidate.rrfScore += 1 / float64(ReciprocalRankK+item.denseRank)
			}
			if item.keywordRank > 0 {
				candidate.keywordRank = item.keywordRank
				candidate.rrfScore += 1 / float64(ReciprocalRankK+item.keywordRank)
			}
		}
	}
	result := make([]rankedCandidate, 0, len(byID))
	for _, candidate := range byID {
		result = append(result, *candidate)
	}
	sort.Slice(result, func(left int, right int) bool {
		if result[left].rrfScore != result[right].rrfScore {
			return result[left].rrfScore > result[right].rrfScore
		}
		leftBest := bestRank(result[left])
		rightBest := bestRank(result[right])
		if leftBest != rightBest {
			return leftBest < rightBest
		}
		return result[left].chunkID < result[right].chunkID
	})
	return result
}

func bestRank(candidate rankedCandidate) int {
	best := CandidateTopK + 1
	if candidate.denseRank > 0 && candidate.denseRank < best {
		best = candidate.denseRank
	}
	if candidate.keywordRank > 0 && candidate.keywordRank < best {
		best = candidate.keywordRank
	}
	return best
}

func normalizedRRF(score float64) float64 {
	maximum := 2 / float64(ReciprocalRankK+1)
	return math.Min(1, score/maximum)
}

func candidateRetrieval(candidate rankedCandidate) string {
	if candidate.denseRank > 0 && candidate.keywordRank > 0 {
		return "dense+bm25"
	}
	if candidate.denseRank > 0 {
		return "dense"
	}
	return "bm25"
}

func aclFilter(tenantID string, userID string) string {
	return fmt.Sprintf("@tenant_id:{%s} @user_id:{%s}", escapeTag(tenantID), escapeTag(userID))
}

func keywordTerms(query string) []string {
	fields := strings.Fields(strings.ToLower(query))
	terms := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = escapeText(field)
		if field == "" {
			continue
		}
		if _, exists := seen[field]; exists {
			continue
		}
		seen[field] = struct{}{}
		terms = append(terms, field)
	}
	return terms
}

func escapeTag(value string) string {
	return escapeRedisSearch(value, true)
}

func escapeText(value string) string {
	return escapeRedisSearch(value, false)
}

func escapeRedisSearch(value string, escapeSpace bool) string {
	const special = `,.<>{}[]"':;!@#$%^&*()-+=~|/\\`
	var builder strings.Builder
	for _, character := range value {
		if strings.ContainsRune(special, character) || (escapeSpace && character == ' ') {
			builder.WriteByte('\\')
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

func float32Vector(vector []float64) []byte {
	result := make([]byte, len(vector)*4)
	for index, value := range vector {
		binary.LittleEndian.PutUint32(result[index*4:], math.Float32bits(float32(value)))
	}
	return result
}
