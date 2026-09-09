package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"GopherAI/model"

	"github.com/google/uuid"
)

const (
	ChunkKindChild       = "child"
	ChunkKindParent      = "parent"
	ParentChunkerVersion = "parent-context-v1"
	parentMaxTokens      = 1200
)

type parentGroup struct {
	scope    string
	sequence int
	children []int
	tokens   int
}

func attachParentChunks(document model.KnowledgeDocument, version model.KnowledgeDocumentVersion, children []model.KnowledgeChunk, now time.Time) []model.KnowledgeChunk {
	if len(children) == 0 {
		return nil
	}
	groups := make([]parentGroup, 0, len(children))
	scopeSequences := make(map[string]int)
	for index := range children {
		children[index].ChunkKind = ChunkKindChild
		scope := parentScope(children[index].SectionPath)
		if len(groups) == 0 || groups[len(groups)-1].scope != scope || groups[len(groups)-1].tokens+children[index].TokenCount > parentMaxTokens {
			sequence := scopeSequences[scope]
			scopeSequences[scope]++
			groups = append(groups, parentGroup{scope: scope, sequence: sequence})
		}
		group := &groups[len(groups)-1]
		group.children = append(group.children, index)
		group.tokens += children[index].TokenCount
	}
	parents := make([]model.KnowledgeChunk, 0, len(groups))
	for groupIndex, group := range groups {
		parts := make([]string, 0, len(group.children))
		for _, childIndex := range group.children {
			parts = append(parts, children[childIndex].Content)
		}
		content := strings.Join(parts, "\n\n")
		digest := sha256.Sum256([]byte(content))
		contentHash := hex.EncodeToString(digest[:])
		seed := fmt.Sprintf("parent|%s|%d|%s|%d|%s", document.ID, version.Version, group.scope, group.sequence, contentHash)
		parentID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed)).String()
		first := children[group.children[0]]
		last := children[group.children[len(group.children)-1]]
		metadata, _ := json.Marshal(map[string]any{
			"document_id": document.ID, "version": version.Version, "scope": group.scope,
			"child_count": len(group.children), "parent_chunker_version": ParentChunkerVersion,
		})
		parent := model.KnowledgeChunk{
			ID: parentID, DocumentID: document.ID, DocumentVersion: version.Version,
			TenantID: document.TenantID, UserID: document.UserID, Ordinal: -(groupIndex + 1),
			SectionPath: group.scope, LineStart: first.LineStart, LineEnd: last.LineEnd,
			Content: content, TokenCount: group.tokens, ContentHash: contentHash, MetadataJSON: string(metadata),
			LogicalKey: logicalChunkKey(version.ParserVersion, "parent:"+group.scope, group.sequence),
			ChunkKind:  ChunkKindParent, IndexStatus: ChunkIndexStatusPending, CreatedAt: now, UpdatedAt: now,
		}
		applyVersionSourceToChunk(&parent, version)
		parents = append(parents, parent)
		for _, childIndex := range group.children {
			children[childIndex].ParentChunkID = parentID
		}
	}
	return parents
}

func parentScope(sectionPath string) string {
	sectionPath = strings.TrimSpace(sectionPath)
	if sectionPath == "" {
		return "document"
	}
	parts := strings.Split(sectionPath, " > ")
	if len(parts) == 1 {
		return strings.TrimSpace(parts[0])
	}
	return strings.TrimSpace(strings.Join(parts[:len(parts)-1], " > "))
}
