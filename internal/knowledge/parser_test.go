package knowledge

import (
	"strings"
	"testing"
)

func TestMarkdownChunkerPreservesHeadingPathAndCodeBlock(t *testing.T) {
	chunker, err := NewStructuredTextChunker(30, 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("# Install\nUse Docker.\n\n## Health\nCheck readiness.\n\n```bash\ndocker ps\ncurl localhost/health\n```\n")
	chunks, err := chunker.ParseAndChunk("runbook.md", content)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected structure-aware chunks, got %d", len(chunks))
	}
	foundCode := false
	for index, chunk := range chunks {
		if chunk.Ordinal != index || chunk.LineStart <= 0 || chunk.LineEnd < chunk.LineStart || len(chunk.ContentHash) != 64 {
			t.Fatalf("invalid chunk metadata: %+v", chunk)
		}
		if strings.Contains(chunk.Content, "docker ps") {
			foundCode = true
			if !strings.Contains(chunk.Content, "```bash\ndocker ps\ncurl localhost/health\n```") {
				t.Fatalf("fenced code block was split: %q", chunk.Content)
			}
			if chunk.SectionPath != "Install > Health" || chunk.LineStart > 7 || chunk.LineEnd < 10 {
				t.Fatalf("code citation metadata is incorrect: %+v", chunk)
			}
		}
	}
	if !foundCode {
		t.Fatal("expected fenced code block in output")
	}
}

func TestTextChunkerUsesBoundedFallbackAndIsDeterministic(t *testing.T) {
	chunker, err := NewStructuredTextChunker(8, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte(strings.Repeat("abcdefgh", 20))
	first, err := chunker.ParseAndChunk("notes.txt", content)
	if err != nil {
		t.Fatal(err)
	}
	second, err := chunker.ParseAndChunk("notes.txt", content)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 2 || len(first) != len(second) {
		t.Fatalf("expected deterministic fallback chunks, got %d and %d", len(first), len(second))
	}
	for index := range first {
		if first[index] != second[index] {
			t.Fatalf("chunk %d changed between runs", index)
		}
		if first[index].TokenCount > 10 {
			t.Fatalf("non-code fallback exceeded max token budget: %+v", first[index])
		}
	}
}

func TestChunkerRejectsUnsupportedAndInvalidUTF8(t *testing.T) {
	chunker := NewDefaultStructuredTextChunker()
	if _, err := chunker.ParseAndChunk("notes.pdf", []byte("text")); err == nil {
		t.Fatal("expected unsupported extension error")
	}
	if _, err := chunker.ParseAndChunk("notes.txt", []byte{0xff, 0xfe}); err == nil {
		t.Fatal("expected invalid UTF-8 error")
	}
}
