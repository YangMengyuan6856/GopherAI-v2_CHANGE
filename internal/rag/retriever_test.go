package rag

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/redis/go-redis/v9"
)

type fakeEmbedder struct {
	vector []float64
	err    error
}

func (embedder *fakeEmbedder) EmbedStrings(context.Context, []string, ...embedding.Option) ([][]float64, error) {
	if embedder.err != nil {
		return nil, embedder.err
	}
	return [][]float64{embedder.vector}, nil
}

type searchCall struct {
	query   string
	options *redis.FTSearchOptions
}

type fakeSearchBackend struct {
	dense      redis.FTSearchResult
	keyword    redis.FTSearchResult
	denseErr   error
	keywordErr error
	calls      []searchCall
}

func (backend *fakeSearchBackend) Search(_ context.Context, _ string, query string, options *redis.FTSearchOptions) (redis.FTSearchResult, error) {
	backend.calls = append(backend.calls, searchCall{query: query, options: options})
	if strings.Contains(query, "KNN") {
		return backend.dense, backend.denseErr
	}
	return backend.keyword, backend.keywordErr
}

type fakeAuthorityRepository struct {
	records  map[string]ChunkAuthority
	tenantID string
	userID   string
}

func (repository *fakeAuthorityRepository) FindAccessibleChunks(_ context.Context, tenantID string, userID string, chunkIDs []string) (map[string]ChunkAuthority, error) {
	repository.tenantID = tenantID
	repository.userID = userID
	result := make(map[string]ChunkAuthority, len(chunkIDs))
	for _, chunkID := range chunkIDs {
		if record, exists := repository.records[chunkID]; exists {
			result[chunkID] = record
		}
	}
	return result, nil
}

func TestHybridRetrieverFusesRanksUsesAuthorityAndDeduplicates(t *testing.T) {
	prefix := "gopher:test:v1:kb:chunk:"
	backend := &fakeSearchBackend{
		dense:   redis.FTSearchResult{Docs: []redis.Document{{ID: prefix + "c1"}, {ID: prefix + "c2"}, {ID: prefix + "c4"}}},
		keyword: redis.FTSearchResult{Docs: []redis.Document{{ID: prefix + "c2"}, {ID: prefix + "c3"}, {ID: prefix + "c4"}}},
	}
	authority := &fakeAuthorityRepository{records: map[string]ChunkAuthority{
		"c1": chunk("c1", "hash-duplicate", "dense evidence"),
		"c2": chunk("c2", "hash-best", "hybrid evidence"),
		"c3": chunk("c3", "hash-keyword", "keyword evidence"),
		"c4": chunk("c4", "hash-duplicate", "duplicate content"),
	}}
	retriever, err := NewHybridRetriever(backend, &fakeEmbedder{vector: []float64{0.1, 0.2}}, authority, "test", 2)
	if err != nil {
		t.Fatal(err)
	}
	output, err := retriever.Search(context.Background(), SearchInput{TenantID: "tenant-a", UserID: "user-a", Query: "REDIS_TIMEOUT retry", TopK: 4})
	if err != nil {
		t.Fatal(err)
	}
	if output.Diagnostics.Mode != "hybrid" || output.Diagnostics.DenseCandidates != 3 || output.Diagnostics.KeywordCandidates != 3 {
		t.Fatalf("unexpected diagnostics: %+v", output.Diagnostics)
	}
	if output.Diagnostics.QueryAssessment.Version != QueryAssessmentVersion {
		t.Fatalf("retrieval must attach an explainable query assessment: %+v", output.Diagnostics.QueryAssessment)
	}
	if len(output.Hits) != 3 {
		t.Fatalf("content-hash dedupe must keep three hits, got %+v", output.Hits)
	}
	if output.Hits[0].Evidence.ID != "c2" || output.Hits[0].Evidence.Retrieval != "dense+bm25" {
		t.Fatalf("candidate present in both lists must rank first: %+v", output.Hits[0])
	}
	if output.Hits[0].Evidence.Title != "project.md" || output.Hits[0].Evidence.Content != "hybrid evidence" {
		t.Fatalf("response must use MySQL authority metadata: %+v", output.Hits[0].Evidence)
	}
	if authority.tenantID != "tenant-a" || authority.userID != "user-a" {
		t.Fatalf("authority lookup lost ACL: tenant=%s user=%s", authority.tenantID, authority.userID)
	}
}

func TestHybridRetrieverAppliesTenantAndUserFiltersToBothRetrievers(t *testing.T) {
	backend := new(fakeSearchBackend)
	authority := &fakeAuthorityRepository{records: map[string]ChunkAuthority{}}
	retriever, err := NewHybridRetriever(backend, &fakeEmbedder{vector: []float64{0.1}}, authority, "test", 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = retriever.Search(context.Background(), SearchInput{TenantID: "team:a", UserID: "user one", Query: "ERR-42", TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.calls) != 2 {
		t.Fatalf("expected dense and keyword searches, got %d", len(backend.calls))
	}
	for _, call := range backend.calls {
		if !strings.Contains(call.query, `@tenant_id:{team\:a}`) || !strings.Contains(call.query, `@user_id:{user\ one}`) {
			t.Fatalf("search query lacks escaped ACL filters: %s", call.query)
		}
	}
	if !strings.Contains(backend.calls[1].query, `err\-42`) {
		t.Fatalf("keyword special characters were not escaped: %s", backend.calls[1].query)
	}
	if !strings.Contains(backend.calls[1].query, `err | 42`) {
		t.Fatalf("identifier components were not expanded for tokenizer compatibility: %s", backend.calls[1].query)
	}
}

func TestHybridRetrieverDegradesToKeywordWhenEmbeddingFails(t *testing.T) {
	prefix := "gopher:test:v1:kb:chunk:"
	backend := &fakeSearchBackend{keyword: redis.FTSearchResult{Docs: []redis.Document{{ID: prefix + "c1"}}}}
	authority := &fakeAuthorityRepository{records: map[string]ChunkAuthority{"c1": chunk("c1", "hash-1", "keyword evidence")}}
	retriever, err := NewHybridRetriever(backend, &fakeEmbedder{err: errors.New("embedding offline")}, authority, "test", 2)
	if err != nil {
		t.Fatal(err)
	}
	output, err := retriever.Search(context.Background(), SearchInput{TenantID: "tenant-a", UserID: "user-a", Query: "ERR42"})
	if err != nil {
		t.Fatal(err)
	}
	if output.Diagnostics.Mode != "bm25_only" || len(output.Hits) != 1 || output.Hits[0].Evidence.Retrieval != "bm25" {
		t.Fatalf("expected keyword degradation, got %+v", output)
	}
}

func TestHybridRetrieverFailsOnlyWhenBothRetrieversFail(t *testing.T) {
	backend := &fakeSearchBackend{keywordErr: errors.New("redis unavailable")}
	retriever, err := NewHybridRetriever(backend, &fakeEmbedder{err: errors.New("embedding unavailable")}, &fakeAuthorityRepository{}, "test", 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = retriever.Search(context.Background(), SearchInput{TenantID: "tenant-a", UserID: "user-a", Query: "question"})
	if !errors.Is(err, ErrRetrievalUnavailable) {
		t.Fatalf("expected retrieval unavailable, got %v", err)
	}
}

func TestHybridRetrieverValidatesQueryAndTopK(t *testing.T) {
	retriever, err := NewHybridRetriever(new(fakeSearchBackend), &fakeEmbedder{vector: []float64{0.1}}, &fakeAuthorityRepository{}, "test", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []SearchInput{
		{TenantID: "tenant", UserID: "user", Query: ""},
		{TenantID: "tenant", UserID: "user", Query: "ok", TopK: MaxTopK + 1},
		{TenantID: "", UserID: "user", Query: "ok"},
	} {
		if _, err := retriever.Search(context.Background(), input); !errors.Is(err, ErrInvalidSearch) {
			t.Fatalf("expected invalid search for %+v, got %v", input, err)
		}
	}
}

func chunk(id string, hash string, content string) ChunkAuthority {
	return ChunkAuthority{
		ID: id, DocumentID: "document-1", DocumentVersion: 1,
		TenantID: "tenant-a", UserID: "user-a", DisplayName: "project.md",
		SectionPath: "Runtime > Redis", LineStart: 10, LineEnd: 14,
		Content: content, ContentHash: hash,
	}
}
