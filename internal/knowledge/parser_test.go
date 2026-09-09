package knowledge

import (
	"os"
	"path/filepath"
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

func TestStructuredDataChunkerPreservesKeyPathsAndLines(t *testing.T) {
	chunker, err := NewStructuredTextChunker(30, 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	jsonContent := []byte("{\n  \"service\": {\n    \"retry\": {\n      \"max_attempts\": 7\n    }\n  }\n}\n")
	jsonChunks, err := chunker.ParseAndChunk("config.json", jsonContent)
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredChunk(t, jsonChunks, "service > retry", "service.retry.max_attempts = 7", 4)

	yamlContent := []byte("services:\n  - name: api\n    port: 9090\nfeature:\n  enabled: true\n")
	yamlChunks, err := chunker.ParseAndChunk("config.yaml", yamlContent)
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredChunk(t, yamlChunks, "services > [0]", "services[0].port = 9090", 3)
	assertStructuredChunk(t, yamlChunks, "feature", "feature.enabled = true", 5)
}

func TestStructuredDataChunkerKeepsSiblingConfigurationFieldsTogether(t *testing.T) {
	chunker, err := NewStructuredTextChunker(30, 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	yamlContent := []byte("service:\n  retry:\n    max_attempts: 6\n    dead_letter_exchange: gopher.jobs.dlx.v1\n")
	chunks, err := chunker.ParseAndChunk("m3b-service.yaml", yamlContent)
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks {
		if chunk.SectionPath == "service > retry" &&
			strings.Contains(chunk.Content, "service.retry.max_attempts = 6") &&
			strings.Contains(chunk.Content, `service.retry.dead_letter_exchange = "gopher.jobs.dlx.v1"`) {
			if chunk.LineStart > 3 || chunk.LineEnd < 4 {
				t.Fatalf("sibling evidence has invalid line range: %+v", chunk)
			}
			return
		}
	}
	t.Fatalf("expected retry siblings in one evidence chunk: %+v", chunks)
}

func TestGoChunkerPreservesTopLevelSymbolsAndSplitsLongFunctions(t *testing.T) {
	chunker, err := NewStructuredTextChunker(12, 16, 2)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte(`package worker

type Service struct{}

func (s *Service) Run(input int) int {
	value := input
	value += 1
	value += 2
	value += 3
	value += 4
	value += 5
	return value
}
`)
	chunks, err := chunker.ParseAndChunk("worker.go", content)
	if err != nil {
		t.Fatal(err)
	}
	foundType := false
	methodChunks := 0
	for _, chunk := range chunks {
		if chunk.TokenCount > 16 {
			t.Fatalf("Go chunk exceeded the configured limit: %+v", chunk)
		}
		if chunk.SectionPath == "package worker > type Service" && strings.Contains(chunk.Content, "type Service") {
			foundType = true
		}
		if chunk.SectionPath == "package worker > method Service.Run" {
			methodChunks++
			if chunk.LineStart < 5 || chunk.LineEnd > 13 {
				t.Fatalf("method line range escaped its declaration: %+v", chunk)
			}
		}
	}
	if !foundType || methodChunks < 2 {
		t.Fatalf("expected a type chunk and multiple long-method chunks, type=%t method_chunks=%d chunks=%+v", foundType, methodChunks, chunks)
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
	for filename, content := range map[string][]byte{
		"invalid.json": []byte(`{"missing": }`),
		"invalid.yaml": []byte("service: [unterminated\n"),
		"invalid.go":   []byte("package broken\nfunc {\n"),
	} {
		if _, err := chunker.ParseAndChunk(filename, content); err == nil {
			t.Fatalf("expected %s parse error", filename)
		}
	}
}

func TestManualStructuredUploadFixturesRemainIndexable(t *testing.T) {
	chunker := NewDefaultStructuredTextChunker()
	for filename, marker := range map[string]string{
		"m3b-config.json":  "Structured-JSON-731",
		"m3b-service.yaml": "Structured-YAML-842",
		"m3b-worker.go":    "Structured-Go-953",
	} {
		content, err := os.ReadFile(filepath.Join("..", "..", "evals", "fixtures", "_manual_uploads", filename))
		if err != nil {
			t.Fatal(err)
		}
		chunks, err := chunker.ParseAndChunk(filename, content)
		if err != nil {
			t.Fatalf("parse %s: %v", filename, err)
		}
		found := false
		for _, chunk := range chunks {
			found = found || strings.Contains(chunk.Content, marker)
		}
		if !found {
			t.Fatalf("fixture %s lost marker %s: %+v", filename, marker, chunks)
		}
	}
}

func assertStructuredChunk(t *testing.T, chunks []ChunkDraft, section string, content string, line int) {
	t.Helper()
	for _, chunk := range chunks {
		if chunk.SectionPath == section && strings.Contains(chunk.Content, content) {
			if chunk.LineStart > line || chunk.LineEnd < line || len(chunk.ContentHash) != 64 {
				t.Fatalf("unexpected structured citation metadata: %+v", chunk)
			}
			return
		}
	}
	t.Fatalf("missing structured chunk section=%q content=%q in %+v", section, content, chunks)
}
