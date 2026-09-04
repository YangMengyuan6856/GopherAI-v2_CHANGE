package knowledge

import (
	"GopherAI/model"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/redis/go-redis/v9"
)

// DefaultEmbeddingBatchSize stays within the Ark/DashScope embedding API limit.
// Keeping the provider limit at the indexing boundary also protects large
// documents, whose chunk count commonly exceeds one request.
const DefaultEmbeddingBatchSize = 10

var safeEnvironment = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

type RedisCommandExecutor interface {
	Do(ctx context.Context, args ...any) *redis.Cmd
}

type RedisChunkIndexer struct {
	client     RedisCommandExecutor
	embedder   embedding.Embedder
	indexName  string
	keyPrefix  string
	dimension  int
	batchSize  int
	indexReady bool
}

func NewRedisChunkIndexer(client RedisCommandExecutor, embedder embedding.Embedder, environment string, dimension int, batchSize int) (*RedisChunkIndexer, error) {
	if client == nil || embedder == nil || dimension <= 0 || batchSize <= 0 {
		return nil, errors.New("redis client, embedder, positive dimension and batch size are required")
	}
	environment = safeEnvironment.ReplaceAllString(strings.TrimSpace(environment), "-")
	if environment == "" {
		environment = "prod"
	}
	base := fmt.Sprintf("gopher:%s:v1:kb", environment)
	return &RedisChunkIndexer{
		client: client, embedder: embedder, dimension: dimension, batchSize: batchSize,
		indexName: base + ":chunks:idx", keyPrefix: base + ":chunk:",
	}, nil
}

func (indexer *RedisChunkIndexer) Index(ctx context.Context, chunks []model.KnowledgeChunk) error {
	if len(chunks) == 0 {
		return errors.New("cannot index an empty chunk set")
	}
	if err := indexer.ensureIndex(ctx); err != nil {
		return err
	}
	for start := 0; start < len(chunks); start += indexer.batchSize {
		end := min(start+indexer.batchSize, len(chunks))
		texts := make([]string, 0, end-start)
		for _, chunk := range chunks[start:end] {
			texts = append(texts, chunk.Content)
		}
		vectors, err := indexer.embedder.EmbedStrings(ctx, texts)
		if err != nil {
			return fmt.Errorf("embed chunk batch: %w", err)
		}
		if len(vectors) != len(texts) {
			return fmt.Errorf("embedding count mismatch: got %d want %d", len(vectors), len(texts))
		}
		for offset, vector := range vectors {
			if len(vector) != indexer.dimension {
				return fmt.Errorf("embedding dimension mismatch: got %d want %d", len(vector), indexer.dimension)
			}
			chunk := chunks[start+offset]
			if err := indexer.client.Do(ctx, "HSET", indexer.keyPrefix+chunk.ID,
				"tenant_id", chunk.TenantID,
				"user_id", chunk.UserID,
				"document_id", chunk.DocumentID,
				"document_version", chunk.DocumentVersion,
				"ordinal", chunk.Ordinal,
				"section_path", chunk.SectionPath,
				"line_start", chunk.LineStart,
				"line_end", chunk.LineEnd,
				"content", chunk.Content,
				"content_hash", chunk.ContentHash,
				"vector", float32Bytes(vector),
			).Err(); err != nil {
				return fmt.Errorf("write redis chunk %s: %w", chunk.ID, err)
			}
		}
	}
	return nil
}

func (indexer *RedisChunkIndexer) Delete(ctx context.Context, chunkIDs []string) error {
	for start := 0; start < len(chunkIDs); start += 100 {
		end := min(start+100, len(chunkIDs))
		args := make([]any, 0, end-start+1)
		args = append(args, "DEL")
		for _, chunkID := range chunkIDs[start:end] {
			if strings.TrimSpace(chunkID) != "" {
				args = append(args, indexer.keyPrefix+chunkID)
			}
		}
		if len(args) == 1 {
			continue
		}
		if err := indexer.client.Do(ctx, args...).Err(); err != nil {
			return fmt.Errorf("delete redis document chunks: %w", err)
		}
	}
	return nil
}

func (indexer *RedisChunkIndexer) ensureIndex(ctx context.Context) error {
	if indexer.indexReady {
		return nil
	}
	if err := indexer.client.Do(ctx, "FT.INFO", indexer.indexName).Err(); err == nil {
		indexer.indexReady = true
		return nil
	} else if !strings.Contains(strings.ToLower(err.Error()), "unknown index") {
		return fmt.Errorf("inspect redis chunk index: %w", err)
	}
	args := []any{
		"FT.CREATE", indexer.indexName,
		"ON", "HASH", "PREFIX", "1", indexer.keyPrefix,
		"SCHEMA",
		"tenant_id", "TAG",
		"user_id", "TAG",
		"document_id", "TAG",
		"document_version", "NUMERIC",
		"ordinal", "NUMERIC",
		"section_path", "TEXT",
		"line_start", "NUMERIC",
		"line_end", "NUMERIC",
		"content", "TEXT",
		"content_hash", "TAG",
		"vector", "VECTOR", "HNSW", "6", "TYPE", "FLOAT32", "DIM", indexer.dimension, "DISTANCE_METRIC", "COSINE",
	}
	if err := indexer.client.Do(ctx, args...).Err(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "index already exists") {
			indexer.indexReady = true
			return nil
		}
		return fmt.Errorf("create redis chunk index: %w", err)
	}
	indexer.indexReady = true
	return nil
}

func float32Bytes(vector []float64) []byte {
	result := make([]byte, len(vector)*4)
	for index, value := range vector {
		binary.LittleEndian.PutUint32(result[index*4:], math.Float32bits(float32(value)))
	}
	return result
}
