package knowledge

import (
	"GopherAI/model"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/redis/go-redis/v9"
)

// DefaultEmbeddingBatchSize stays within the Ark/DashScope embedding API limit.
// Keeping the provider limit at the indexing boundary also protects large
// documents, whose chunk count commonly exceeds one request.
const DefaultEmbeddingBatchSize = 10

const embeddingCacheTTLSeconds = 7 * 24 * 60 * 60

var safeEnvironment = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

type RedisCommandExecutor interface {
	Do(ctx context.Context, args ...any) *redis.Cmd
}

type RedisChunkIndexer struct {
	client      RedisCommandExecutor
	embedder    embedding.Embedder
	indexName   string
	keyPrefix   string
	cachePrefix string
	dimension   int
	batchSize   int
	indexReady  bool
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
		indexName: base + ":chunks:idx", keyPrefix: base + ":chunk:", cachePrefix: base + ":embedding-cache:",
	}, nil
}

func (indexer *RedisChunkIndexer) Index(ctx context.Context, chunks []model.KnowledgeChunk) error {
	_, err := indexer.IndexIncremental(ctx, chunks)
	return err
}

type VectorIndexStats struct {
	EmbeddedChunks int
	ReusedVectors  int
	CacheHits      int
	PreviousHits   int
}

func (indexer *RedisChunkIndexer) IndexIncremental(ctx context.Context, chunks []model.KnowledgeChunk) (VectorIndexStats, error) {
	stats := VectorIndexStats{}
	if len(chunks) == 0 {
		return stats, errors.New("cannot index an empty chunk set")
	}
	if err := indexer.ensureIndex(ctx); err != nil {
		return stats, err
	}
	vectors := make([][]byte, len(chunks))
	missing := make([]int, 0, len(chunks))
	for index, chunk := range chunks {
		if vector, ok := indexer.cachedVector(ctx, chunk); ok {
			vectors[index] = vector
			stats.ReusedVectors++
			stats.CacheHits++
			continue
		}
		if vector, ok := indexer.previousVector(ctx, chunk.EmbeddingSourceChunkID); ok {
			vectors[index] = vector
			stats.ReusedVectors++
			stats.PreviousHits++
			indexer.rememberVector(ctx, chunk, vector)
			continue
		}
		missing = append(missing, index)
	}
	for start := 0; start < len(missing); start += indexer.batchSize {
		end := min(start+indexer.batchSize, len(missing))
		texts := make([]string, 0, end-start)
		for _, index := range missing[start:end] {
			texts = append(texts, chunks[index].Content)
		}
		embeddings, err := indexer.embedder.EmbedStrings(ctx, texts)
		if err != nil {
			return stats, fmt.Errorf("embed chunk batch: %w", err)
		}
		if len(embeddings) != len(texts) {
			return stats, fmt.Errorf("embedding count mismatch: got %d want %d", len(embeddings), len(texts))
		}
		for offset, vector := range embeddings {
			if len(vector) != indexer.dimension {
				return stats, fmt.Errorf("embedding dimension mismatch: got %d want %d", len(vector), indexer.dimension)
			}
			chunkIndex := missing[start+offset]
			vectors[chunkIndex] = float32Bytes(vector)
			stats.EmbeddedChunks++
			indexer.rememberVector(ctx, chunks[chunkIndex], vectors[chunkIndex])
		}
	}
	for index, chunk := range chunks {
		if len(vectors[index]) != indexer.dimension*4 {
			return stats, fmt.Errorf("indexed vector size mismatch: got %d want %d", len(vectors[index]), indexer.dimension*4)
		}
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
			"logical_key", chunk.LogicalKey,
			"chunk_kind", chunk.ChunkKind,
			"parent_chunk_id", chunk.ParentChunkID,
			"source_kind", chunk.SourceKind,
			"source_revision", chunk.SourceRevision,
			"authority", chunk.Authority,
			"effective_at", unixTimeOrZero(chunk.EffectiveAt),
			"expired_at", unixTimeOrZero(chunk.ExpiredAt),
			"supersedes_version", chunk.SupersedesVersion,
			"vector", vectors[index],
		).Err(); err != nil {
			return stats, fmt.Errorf("write redis chunk %s: %w", chunk.ID, err)
		}
	}
	return stats, nil
}

func (indexer *RedisChunkIndexer) cachedVector(ctx context.Context, chunk model.KnowledgeChunk) ([]byte, bool) {
	if strings.TrimSpace(chunk.ContentHash) == "" || strings.TrimSpace(chunk.EmbeddingVersion) == "" {
		return nil, false
	}
	return indexer.readVector(ctx, "GET", indexer.embeddingCacheKey(chunk))
}

func (indexer *RedisChunkIndexer) previousVector(ctx context.Context, sourceChunkID string) ([]byte, bool) {
	if strings.TrimSpace(sourceChunkID) == "" {
		return nil, false
	}
	return indexer.readVector(ctx, "HGET", indexer.keyPrefix+sourceChunkID, "vector")
}

func (indexer *RedisChunkIndexer) readVector(ctx context.Context, command string, args ...any) ([]byte, bool) {
	commandArgs := append([]any{command}, args...)
	result, err := indexer.client.Do(ctx, commandArgs...).Result()
	if err != nil {
		return nil, false
	}
	var value []byte
	switch typed := result.(type) {
	case []byte:
		value = typed
	case string:
		value = []byte(typed)
	default:
		return nil, false
	}
	if len(value) != indexer.dimension*4 {
		return nil, false
	}
	return append([]byte(nil), value...), true
}

func (indexer *RedisChunkIndexer) rememberVector(ctx context.Context, chunk model.KnowledgeChunk, vector []byte) {
	if strings.TrimSpace(chunk.ContentHash) == "" || strings.TrimSpace(chunk.EmbeddingVersion) == "" || len(vector) != indexer.dimension*4 {
		return
	}
	_ = indexer.client.Do(ctx, "SET", indexer.embeddingCacheKey(chunk), vector, "EX", embeddingCacheTTLSeconds).Err()
}

func (indexer *RedisChunkIndexer) embeddingCacheKey(chunk model.KnowledgeChunk) string {
	digest := sha256.Sum256([]byte(chunk.TenantID + "\x00" + chunk.UserID + "\x00" + chunk.EmbeddingVersion + "\x00" + chunk.ContentHash))
	return indexer.cachePrefix + hex.EncodeToString(digest[:])
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

func unixTimeOrZero(value *time.Time) int64 {
	if value == nil {
		return 0
	}
	return value.UTC().Unix()
}

func float32Bytes(vector []float64) []byte {
	result := make([]byte, len(vector)*4)
	for index, value := range vector {
		binary.LittleEndian.PutUint32(result[index*4:], math.Float32bits(float32(value)))
	}
	return result
}
