package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"GopherAI/model"
)

const RevisionIndexStatsVersion = "revision-diff-v1"

type RevisionIndexStats struct {
	Version         string `json:"version"`
	ChunkCount      int    `json:"chunk_count"`
	AddedChunks     int    `json:"added_chunks"`
	ModifiedChunks  int    `json:"modified_chunks"`
	DeletedChunks   int    `json:"deleted_chunks"`
	UnchangedChunks int    `json:"unchanged_chunks"`
	EmbeddedChunks  int    `json:"embedded_chunks"`
	ReusedVectors   int    `json:"reused_vectors"`
}

func assignLogicalChunkKeys(parserVersion string, chunks []model.KnowledgeChunk) {
	occurrences := make(map[string]int)
	for index := range chunks {
		identity := normalizedChunkIdentity(chunks[index].SectionPath, chunks[index].Ordinal)
		occurrence := occurrences[identity]
		occurrences[identity]++
		chunks[index].LogicalKey = logicalChunkKey(parserVersion, identity, occurrence)
	}
}

func analyzeRevision(previous []model.KnowledgeChunk, current []model.KnowledgeChunk, parserVersion string) RevisionIndexStats {
	stats := RevisionIndexStats{Version: RevisionIndexStatsVersion, ChunkCount: len(current)}
	if len(previous) == 0 {
		stats.AddedChunks = len(current)
		return stats
	}
	previousCopy := append([]model.KnowledgeChunk(nil), previous...)
	sort.SliceStable(previousCopy, func(left, right int) bool { return previousCopy[left].Ordinal < previousCopy[right].Ordinal })
	assignMissingLogicalChunkKeys(parserVersion, previousCopy)
	previousByKey := make(map[string]model.KnowledgeChunk, len(previousCopy))
	previousByHash := make(map[string]model.KnowledgeChunk, len(previousCopy))
	for _, chunk := range previousCopy {
		previousByKey[chunk.LogicalKey] = chunk
		if chunk.ContentHash != "" {
			if _, exists := previousByHash[chunk.ContentHash]; !exists {
				previousByHash[chunk.ContentHash] = chunk
			}
		}
	}
	seenKeys := make(map[string]struct{}, len(current))
	for index := range current {
		chunk := &current[index]
		seenKeys[chunk.LogicalKey] = struct{}{}
		if prior, exists := previousByKey[chunk.LogicalKey]; exists {
			if prior.ContentHash == chunk.ContentHash {
				stats.UnchangedChunks++
				if reusableEmbedding(prior, *chunk) {
					chunk.EmbeddingSourceChunkID = prior.ID
				}
			} else {
				stats.ModifiedChunks++
			}
		} else {
			stats.AddedChunks++
		}
		if strings.TrimSpace(chunk.EmbeddingSourceChunkID) == "" && chunk.ContentHash != "" {
			if prior, exists := previousByHash[chunk.ContentHash]; exists && reusableEmbedding(prior, *chunk) {
				chunk.EmbeddingSourceChunkID = prior.ID
			}
		}
	}
	for key := range previousByKey {
		if _, exists := seenKeys[key]; !exists {
			stats.DeletedChunks++
		}
	}
	return stats
}

func reusableEmbedding(previous, current model.KnowledgeChunk) bool {
	return strings.TrimSpace(previous.EmbeddingVersion) != "" && previous.EmbeddingVersion == current.EmbeddingVersion &&
		previous.ContentHash != "" && previous.ContentHash == current.ContentHash
}

func assignMissingLogicalChunkKeys(parserVersion string, chunks []model.KnowledgeChunk) {
	occurrences := make(map[string]int)
	for index := range chunks {
		identity := normalizedChunkIdentity(chunks[index].SectionPath, chunks[index].Ordinal)
		occurrence := occurrences[identity]
		occurrences[identity]++
		if strings.TrimSpace(chunks[index].LogicalKey) == "" {
			chunks[index].LogicalKey = logicalChunkKey(parserVersion, identity, occurrence)
		}
	}
}

func normalizedChunkIdentity(sectionPath string, ordinal int) string {
	sectionPath = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(sectionPath)), " "))
	if sectionPath == "" {
		return fmt.Sprintf("ordinal:%d", ordinal)
	}
	return "section:" + sectionPath
}

func logicalChunkKey(parserVersion string, identity string, occurrence int) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(parserVersion) + "\x00" + identity + fmt.Sprintf("\x00%d", occurrence)))
	return hex.EncodeToString(digest[:])
}

func applyRevisionStats(version *model.KnowledgeDocumentVersion, stats RevisionIndexStats) {
	if version == nil {
		return
	}
	version.IndexStatsVersion = stats.Version
	version.IndexChunkCount = stats.ChunkCount
	version.IndexAddedChunks = stats.AddedChunks
	version.IndexModifiedChunks = stats.ModifiedChunks
	version.IndexDeletedChunks = stats.DeletedChunks
	version.IndexUnchangedChunks = stats.UnchangedChunks
	version.IndexEmbeddedChunks = stats.EmbeddedChunks
	version.IndexReusedVectors = stats.ReusedVectors
}
