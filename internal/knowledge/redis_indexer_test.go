package knowledge

import (
	"GopherAI/model"
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/redis/go-redis/v9"
)

type redisCommandCall struct {
	args []any
}

type fakeRedisExecutor struct {
	calls       []redisCommandCall
	values      map[string][]byte
	hashVectors map[string][]byte
}

func (executor *fakeRedisExecutor) Do(ctx context.Context, args ...any) *redis.Cmd {
	executor.calls = append(executor.calls, redisCommandCall{args: append([]any(nil), args...)})
	command := redis.NewCmd(ctx, args...)
	if args[0] == "FT.INFO" && countCommand(executor.calls, "FT.CREATE") == 0 {
		command.SetErr(errors.New("Unknown index name"))
		return command
	}
	switch args[0] {
	case "EXISTS":
		var count int64
		for _, rawKey := range args[1:] {
			if _, exists := executor.hashVectors[rawKey.(string)]; exists {
				count++
			}
		}
		command.SetVal(count)
		return command
	case "GET":
		if value, exists := executor.values[args[1].(string)]; exists {
			command.SetVal(append([]byte(nil), value...))
		} else {
			command.SetErr(redis.Nil)
		}
		return command
	case "HGET":
		if value, exists := executor.hashVectors[args[1].(string)]; exists {
			command.SetVal(append([]byte(nil), value...))
		} else {
			command.SetErr(redis.Nil)
		}
		return command
	case "SET":
		if executor.values == nil {
			executor.values = make(map[string][]byte)
		}
		executor.values[args[1].(string)] = append([]byte(nil), args[2].([]byte)...)
	case "HSET":
		if executor.hashVectors == nil {
			executor.hashVectors = make(map[string][]byte)
		}
		for index := 2; index+1 < len(args); index += 2 {
			if args[index] == "vector" {
				executor.hashVectors[args[1].(string)] = append([]byte(nil), args[index+1].([]byte)...)
				break
			}
		}
	}
	command.SetVal("OK")
	return command
}

type fakeEmbedder struct {
	vectors [][]float64
	err     error
	texts   []string
}

func (embedder *fakeEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	embedder.texts = append([]string(nil), texts...)
	return embedder.vectors, embedder.err
}

type batchRecordingEmbedder struct {
	callSizes []int
}

func (embedder *batchRecordingEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	embedder.callSizes = append(embedder.callSizes, len(texts))
	vectors := make([][]float64, len(texts))
	for index := range texts {
		vectors[index] = []float64{float64(index), 1}
	}
	return vectors, nil
}

func TestRedisChunkIndexerCreatesTenantFilteredSchemaAndWritesVectors(t *testing.T) {
	client := new(fakeRedisExecutor)
	embedder := &fakeEmbedder{vectors: [][]float64{{0.25, -0.5}}}
	indexer, err := NewRedisChunkIndexer(client, embedder, "staging/test", 2, 16)
	if err != nil {
		t.Fatal(err)
	}
	chunk := model.KnowledgeChunk{
		ID: "chunk-1", TenantID: "tenant-a", UserID: "user-a", DocumentID: "document-1",
		DocumentVersion: 1, Ordinal: 0, SectionPath: "Config", LineStart: 1, LineEnd: 2,
		Content: "retry count is seven", ContentHash: "hash-1",
	}
	if err := indexer.Index(context.Background(), []model.KnowledgeChunk{chunk}); err != nil {
		t.Fatal(err)
	}
	if countCommand(client.calls, "FT.CREATE") != 1 || countCommand(client.calls, "HSET") != 1 {
		t.Fatalf("unexpected redis commands: %+v", client.calls)
	}
	create := findCommand(client.calls, "FT.CREATE")
	if !containsArgument(create, "tenant_id") || !containsArgument(create, "TAG") || !containsArgument(create, "vector") || !containsArgument(create, 2) {
		t.Fatalf("index schema lacks ACL or vector fields: %v", create)
	}
	hset := findCommand(client.calls, "HSET")
	if hset[1] != "gopher:staging-test:v1:kb:chunk:chunk-1" || !containsArgument(hset, "tenant-a") {
		t.Fatalf("unexpected redis chunk key or ACL: %v", hset)
	}
	vector, ok := hset[len(hset)-1].([]byte)
	if !ok || len(vector) != 8 {
		t.Fatalf("expected two FLOAT32 values, got %T length %d", hset[len(hset)-1], len(vector))
	}
}

func TestRedisChunkIndexerRejectsEmbeddingDimensionMismatch(t *testing.T) {
	client := new(fakeRedisExecutor)
	indexer, err := NewRedisChunkIndexer(client, &fakeEmbedder{vectors: [][]float64{{0.1}}}, "test", 2, 16)
	if err != nil {
		t.Fatal(err)
	}
	err = indexer.Index(context.Background(), []model.KnowledgeChunk{{ID: "chunk", Content: "content"}})
	if err == nil || countCommand(client.calls, "HSET") != 0 {
		t.Fatalf("dimension mismatch must fail before Redis write: %v", err)
	}
}

func TestRedisChunkIndexerKeepsEmbeddingRequestsWithinProviderLimit(t *testing.T) {
	client := new(fakeRedisExecutor)
	embedder := new(batchRecordingEmbedder)
	indexer, err := NewRedisChunkIndexer(client, embedder, "test", 2, DefaultEmbeddingBatchSize)
	if err != nil {
		t.Fatal(err)
	}
	chunks := make([]model.KnowledgeChunk, 11)
	for index := range chunks {
		chunks[index] = model.KnowledgeChunk{ID: fmt.Sprintf("chunk-%d", index), Content: "content"}
	}
	if err := indexer.Index(context.Background(), chunks); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(embedder.callSizes, []int{10, 1}) {
		t.Fatalf("unexpected embedding batch sizes: %v", embedder.callSizes)
	}
	if countCommand(client.calls, "HSET") != len(chunks) {
		t.Fatalf("expected %d indexed chunks, got %d", len(chunks), countCommand(client.calls, "HSET"))
	}
}

func TestRedisChunkIndexerReusesTenantScopedEmbeddingCache(t *testing.T) {
	client := new(fakeRedisExecutor)
	embedder := &fakeEmbedder{vectors: [][]float64{{0.25, -0.5}}}
	indexer, err := NewRedisChunkIndexer(client, embedder, "test", 2, DefaultEmbeddingBatchSize)
	if err != nil {
		t.Fatal(err)
	}
	first := model.KnowledgeChunk{
		ID: "chunk-v1", TenantID: "tenant-a", UserID: "user-a", Content: "same content",
		ContentHash: "same-hash", EmbeddingVersion: "embedding-v1",
	}
	firstStats, err := indexer.IndexIncremental(context.Background(), []model.KnowledgeChunk{first})
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID = "chunk-v2"
	secondStats, err := indexer.IndexIncremental(context.Background(), []model.KnowledgeChunk{second})
	if err != nil {
		t.Fatal(err)
	}
	if firstStats.EmbeddedChunks != 1 || firstStats.ReusedVectors != 0 || secondStats.CacheHits != 1 || secondStats.EmbeddedChunks != 0 {
		t.Fatalf("unexpected incremental cache stats: first=%+v second=%+v", firstStats, secondStats)
	}
	if len(embedder.texts) != 1 || countCommand(client.calls, "SET") != 1 || countCommand(client.calls, "HSET") != 2 {
		t.Fatalf("unchanged content should be embedded once: texts=%v calls=%+v", embedder.texts, client.calls)
	}
}

func TestRedisChunkIndexerFallsBackToPreviousChunkVector(t *testing.T) {
	client := new(fakeRedisExecutor)
	embedder := &fakeEmbedder{vectors: [][]float64{{0.25, -0.5}}}
	indexer, err := NewRedisChunkIndexer(client, embedder, "test", 2, DefaultEmbeddingBatchSize)
	if err != nil {
		t.Fatal(err)
	}
	oldChunk := model.KnowledgeChunk{
		ID: "chunk-v1", TenantID: "tenant-a", UserID: "user-a", Content: "same content",
		ContentHash: "same-hash", EmbeddingVersion: "embedding-v1",
	}
	if _, err := indexer.IndexIncremental(context.Background(), []model.KnowledgeChunk{oldChunk}); err != nil {
		t.Fatal(err)
	}
	client.values = nil // simulate deployment before the content-addressed cache existed
	newChunk := oldChunk
	newChunk.ID = "chunk-v2"
	newChunk.EmbeddingSourceChunkID = oldChunk.ID
	stats, err := indexer.IndexIncremental(context.Background(), []model.KnowledgeChunk{newChunk})
	if err != nil {
		t.Fatal(err)
	}
	if stats.PreviousHits != 1 || stats.ReusedVectors != 1 || stats.EmbeddedChunks != 0 {
		t.Fatalf("expected previous authoritative chunk vector reuse, got %+v", stats)
	}
	if len(embedder.texts) != 1 {
		t.Fatalf("fallback reuse unexpectedly called embedder again: %v", embedder.texts)
	}
}

func TestRedisChunkIndexerDeletesExactChunkKeysInBoundedBatches(t *testing.T) {
	client := new(fakeRedisExecutor)
	indexer, err := NewRedisChunkIndexer(client, new(fakeEmbedder), "test", 2, DefaultEmbeddingBatchSize)
	if err != nil {
		t.Fatal(err)
	}
	chunkIDs := make([]string, 205)
	for index := range chunkIDs {
		chunkIDs[index] = fmt.Sprintf("chunk-%d", index)
	}
	if err := indexer.Delete(context.Background(), chunkIDs); err != nil {
		t.Fatal(err)
	}
	if countCommand(client.calls, "DEL") != 3 {
		t.Fatalf("expected 100/100/5 delete batches, got %+v", client.calls)
	}
	first := findCommand(client.calls, "DEL")
	if len(first) != 101 || first[1] != "gopher:test:v1:kb:chunk:chunk-0" || first[100] != "gopher:test:v1:kb:chunk:chunk-99" {
		t.Fatalf("delete must target exact namespaced keys: %+v", first)
	}
}

func TestRedisChunkIndexerCountsPresentAuthoritativeKeys(t *testing.T) {
	client := &fakeRedisExecutor{hashVectors: map[string][]byte{
		"gopher:test:v1:kb:chunk:chunk-1": {1},
		"gopher:test:v1:kb:chunk:chunk-3": {1},
	}}
	indexer, err := NewRedisChunkIndexer(client, new(fakeEmbedder), "test", 2, DefaultEmbeddingBatchSize)
	if err != nil {
		t.Fatal(err)
	}
	chunks := []model.KnowledgeChunk{{ID: "chunk-1"}, {ID: "chunk-2"}, {ID: "chunk-3"}}
	present, err := indexer.PresentChunkCount(context.Background(), chunks)
	if err != nil {
		t.Fatal(err)
	}
	if present != 2 || countCommand(client.calls, "EXISTS") != 1 {
		t.Fatalf("present=%d calls=%+v", present, client.calls)
	}
}

func countCommand(calls []redisCommandCall, name string) int {
	count := 0
	for _, call := range calls {
		if len(call.args) > 0 && call.args[0] == name {
			count++
		}
	}
	return count
}

func findCommand(calls []redisCommandCall, name string) []any {
	for _, call := range calls {
		if len(call.args) > 0 && call.args[0] == name {
			return call.args
		}
	}
	return nil
}

func containsArgument(args []any, expected any) bool {
	for _, argument := range args {
		if reflect.DeepEqual(argument, expected) {
			return true
		}
	}
	return false
}
