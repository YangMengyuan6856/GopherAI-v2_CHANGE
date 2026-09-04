package rag

import (
	"GopherAI/internal/contract"
	"errors"
	"testing"
)

func TestCitationBuilderVerifiesACLAndRewritesInlineReferences(t *testing.T) {
	evidence := []contract.Evidence{
		{ID: "chunk-1", TenantID: "tenant-a", SourceID: "doc-1", SourceVersion: "2", Title: "runbook.md", Section: "Retry", LineStart: 8, LineEnd: 12, Content: "retry is 7"},
		{ID: "chunk-2", TenantID: "tenant-a", SourceID: "doc-2", SourceVersion: "1", Title: "override.md", Section: "Production", LineStart: 3, LineEnd: 4, Content: "retry is 9"},
	}
	answer, citations, err := NewCitationBuilder().BuildAndVerify("tenant-a", "文档分别写明 7 [E1] 和 9 [E2]。", []string{"E1", "E2"}, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if answer != "文档分别写明 7 [1] 和 9 [2]。" || len(citations) != 2 || citations[1].LineStart != 3 {
		t.Fatalf("unexpected verified citations: answer=%q citations=%+v", answer, citations)
	}
}

func TestCitationBuilderRejectsUnknownMissingAndUnauthorizedReferences(t *testing.T) {
	valid := []contract.Evidence{{ID: "c1", TenantID: "tenant-a", SourceID: "d1", SourceVersion: "1", Title: "a.md", LineStart: 1, LineEnd: 1, Content: "answer"}}
	for _, test := range []struct {
		answer   string
		refs     []string
		evidence []contract.Evidence
	}{
		{"没有标记", []string{"E1"}, valid},
		{"错误标记 [E2]", []string{"E2"}, valid},
		{"越权 [E1]", []string{"E1"}, []contract.Evidence{{ID: "c1", TenantID: "tenant-b", SourceID: "d1", SourceVersion: "1", Title: "a.md", LineStart: 1, LineEnd: 1, Content: "answer"}}},
	} {
		if _, _, err := NewCitationBuilder().BuildAndVerify("tenant-a", test.answer, test.refs, test.evidence); !errors.Is(err, ErrCitationVerification) {
			t.Fatalf("expected citation verification failure, got %v", err)
		}
	}
}
