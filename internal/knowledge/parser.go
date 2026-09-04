package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultChunkTargetTokens = 600
	DefaultChunkMaxTokens    = 800
	DefaultChunkOverlap      = 80
)

type ChunkDraft struct {
	Ordinal     int
	SectionPath string
	LineStart   int
	LineEnd     int
	Content     string
	TokenCount  int
	ContentHash string
}

type ParserChunker interface {
	ParseAndChunk(filename string, content []byte) ([]ChunkDraft, error)
}

type StructuredTextChunker struct {
	targetTokens  int
	maxTokens     int
	overlapTokens int
}

type sourceBlock struct {
	sectionPath string
	lineStart   int
	lineEnd     int
	content     string
	tokenCount  int
	code        bool
}

func NewStructuredTextChunker(targetTokens int, maxTokens int, overlapTokens int) (*StructuredTextChunker, error) {
	if targetTokens <= 0 || maxTokens < targetTokens || overlapTokens < 0 || overlapTokens >= targetTokens {
		return nil, fmt.Errorf("invalid chunk configuration")
	}
	return &StructuredTextChunker{targetTokens: targetTokens, maxTokens: maxTokens, overlapTokens: overlapTokens}, nil
}

func NewDefaultStructuredTextChunker() *StructuredTextChunker {
	chunker, err := NewStructuredTextChunker(DefaultChunkTargetTokens, DefaultChunkMaxTokens, DefaultChunkOverlap)
	if err != nil {
		panic(err)
	}
	return chunker
}

func (chunker *StructuredTextChunker) ParseAndChunk(filename string, content []byte) ([]ChunkDraft, error) {
	if !utf8.Valid(content) {
		return nil, fmt.Errorf("document is not valid UTF-8")
	}
	extension := strings.ToLower(filepath.Ext(filename))
	if extension != ".md" && extension != ".txt" {
		return nil, fmt.Errorf("unsupported document extension %q", extension)
	}
	blocks := parseTextBlocks(string(content), extension == ".md")
	if len(blocks) == 0 {
		return nil, fmt.Errorf("document has no indexable text")
	}
	blocks = chunker.splitOversizedBlocks(blocks)
	return chunker.buildChunks(blocks), nil
}

func parseTextBlocks(content string, markdown bool) []sourceBlock {
	normalized := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	blocks := make([]sourceBlock, 0)
	headings := make([]string, 0, 6)
	paragraph := make([]string, 0)
	paragraphStart := 0
	inCode := false
	codeLines := make([]string, 0)
	codeStart := 0

	sectionPath := func() string { return strings.Join(headings, " > ") }
	flushParagraph := func(lineEnd int) {
		if len(paragraph) == 0 {
			return
		}
		text := strings.TrimSpace(strings.Join(paragraph, "\n"))
		if text != "" {
			blocks = append(blocks, newSourceBlock(sectionPath(), paragraphStart, lineEnd, text, false))
		}
		paragraph = paragraph[:0]
		paragraphStart = 0
	}
	flushCode := func(lineEnd int) {
		if len(codeLines) == 0 {
			return
		}
		blocks = append(blocks, newSourceBlock(sectionPath(), codeStart, lineEnd, strings.Join(codeLines, "\n"), true))
		codeLines = codeLines[:0]
		codeStart = 0
	}

	for index, line := range lines {
		lineNumber := index + 1
		trimmed := strings.TrimSpace(line)
		if markdown && strings.HasPrefix(trimmed, "```") {
			if inCode {
				codeLines = append(codeLines, line)
				flushCode(lineNumber)
				inCode = false
			} else {
				flushParagraph(lineNumber - 1)
				inCode = true
				codeStart = lineNumber
				codeLines = append(codeLines, line)
			}
			continue
		}
		if inCode {
			codeLines = append(codeLines, line)
			continue
		}
		if markdown {
			if level, title, ok := markdownHeading(trimmed); ok {
				flushParagraph(lineNumber - 1)
				if len(headings) >= level {
					headings = headings[:level-1]
				}
				for len(headings) < level-1 {
					headings = append(headings, "")
				}
				headings = append(headings, title)
				blocks = append(blocks, newSourceBlock(sectionPath(), lineNumber, lineNumber, line, false))
				continue
			}
		}
		if trimmed == "" {
			flushParagraph(lineNumber - 1)
			continue
		}
		if paragraphStart == 0 {
			paragraphStart = lineNumber
		}
		paragraph = append(paragraph, line)
	}
	if inCode {
		flushCode(len(lines))
	} else {
		flushParagraph(len(lines))
	}
	return blocks
}

func markdownHeading(line string) (int, string, bool) {
	level := 0
	for level < len(line) && level < 6 && line[level] == '#' {
		level++
	}
	if level == 0 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	title := strings.TrimSpace(line[level+1:])
	return level, title, title != ""
}

func newSourceBlock(section string, lineStart int, lineEnd int, content string, code bool) sourceBlock {
	return sourceBlock{sectionPath: section, lineStart: lineStart, lineEnd: lineEnd, content: content, tokenCount: estimateTokens(content), code: code}
}

func (chunker *StructuredTextChunker) splitOversizedBlocks(blocks []sourceBlock) []sourceBlock {
	result := make([]sourceBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.code || block.tokenCount <= chunker.maxTokens {
			result = append(result, block)
			continue
		}
		result = append(result, splitTextBlock(block, chunker.targetTokens)...)
	}
	return result
}

func splitTextBlock(block sourceBlock, targetTokens int) []sourceBlock {
	runes := []rune(block.content)
	if len(runes) == 0 {
		return nil
	}
	result := make([]sourceBlock, 0)
	start := 0
	for start < len(runes) {
		end := start
		for end < len(runes) && estimateTokens(string(runes[start:end+1])) <= targetTokens {
			end++
		}
		if end == start {
			end++
		}
		piece := strings.TrimSpace(string(runes[start:end]))
		if piece != "" {
			result = append(result, newSourceBlock(block.sectionPath, block.lineStart, block.lineEnd, piece, false))
		}
		start = end
	}
	return result
}

func (chunker *StructuredTextChunker) buildChunks(blocks []sourceBlock) []ChunkDraft {
	result := make([]ChunkDraft, 0)
	current := make([]sourceBlock, 0)
	currentTokens := 0
	hasNewContent := false
	flush := func() {
		if len(current) == 0 || !hasNewContent {
			return
		}
		result = append(result, makeChunkDraft(len(result), current))
		current, currentTokens = overlapBlocks(current, chunker.overlapTokens)
		hasNewContent = false
	}
	for _, block := range blocks {
		sectionChanged := len(current) > 0 && current[0].sectionPath != block.sectionPath
		wouldExceed := len(current) > 0 && currentTokens+block.tokenCount > chunker.maxTokens
		if sectionChanged || wouldExceed {
			flush()
			if sectionChanged {
				current = current[:0]
				currentTokens = 0
				hasNewContent = false
			}
		}
		current = append(current, block)
		currentTokens += block.tokenCount
		hasNewContent = true
		if currentTokens >= chunker.targetTokens || block.code && block.tokenCount > chunker.maxTokens {
			flush()
		}
	}
	if len(current) > 0 && hasNewContent {
		result = append(result, makeChunkDraft(len(result), current))
	}
	return result
}

func overlapBlocks(blocks []sourceBlock, limit int) ([]sourceBlock, int) {
	if limit <= 0 {
		return nil, 0
	}
	tokens := 0
	start := len(blocks)
	for start > 0 && tokens+blocks[start-1].tokenCount <= limit {
		start--
		tokens += blocks[start].tokenCount
	}
	if start == len(blocks) {
		return nil, 0
	}
	result := append([]sourceBlock(nil), blocks[start:]...)
	return result, tokens
}

func makeChunkDraft(ordinal int, blocks []sourceBlock) ChunkDraft {
	parts := make([]string, 0, len(blocks))
	tokens := 0
	for _, block := range blocks {
		parts = append(parts, block.content)
		tokens += block.tokenCount
	}
	content := strings.Join(parts, "\n\n")
	hash := sha256.Sum256([]byte(content))
	return ChunkDraft{
		Ordinal: ordinal, SectionPath: blocks[0].sectionPath,
		LineStart: blocks[0].lineStart, LineEnd: blocks[len(blocks)-1].lineEnd,
		Content: content, TokenCount: tokens, ContentHash: hex.EncodeToString(hash[:]),
	}
}

func estimateTokens(content string) int {
	if content == "" {
		return 0
	}
	tokens := 0
	latinRunes := 0
	flushLatin := func() {
		if latinRunes > 0 {
			tokens += (latinRunes + 3) / 4
			latinRunes = 0
		}
	}
	for _, character := range content {
		if unicode.Is(unicode.Han, character) || unicode.Is(unicode.Hiragana, character) || unicode.Is(unicode.Katakana, character) {
			flushLatin()
			tokens++
			continue
		}
		if unicode.IsSpace(character) || unicode.IsPunct(character) || unicode.IsSymbol(character) {
			flushLatin()
			if !unicode.IsSpace(character) {
				tokens++
			}
			continue
		}
		latinRunes++
	}
	flushLatin()
	if tokens == 0 {
		return 1
	}
	return tokens
}
