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
	calls []redisCommandCall
}

func (executor *fakeRedisExecutor) Do(ctx context.Context, args ...any) *redis.Cmd {
	executor.calls = append(executor.calls, redisCommandCall{args: append([]any(nil), args...)})
	command := redis.NewCmd(ctx, args...)
	if args[0] == "FT.INFO" && countCommand(executor.calls, "FT.CREATE") == 0 {
		command.SetErr(errors.New("Unknown index name"))
		return command
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
