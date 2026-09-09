package main

import (
	"GopherAI/internal/rag"
	"context"
	"testing"
)

type countingSearcher struct {
	calls int
}

func (searcher *countingSearcher) Search(_ context.Context, input rag.SearchInput) (rag.SearchOutput, error) {
	searcher.calls++
	return rag.SearchOutput{Diagnostics: rag.SearchDiagnostics{Mode: input.Query}}, nil
}

func TestCachedSearcherReusesIdenticalSuccessfulQuery(t *testing.T) {
	inner := new(countingSearcher)
	searcher := newCachedSearcher(inner)
	input := rag.SearchInput{TenantID: "tenant", UserID: "user", Query: "question", TopK: 5}
	for range 2 {
		output, err := searcher.Search(context.Background(), input)
		if err != nil || output.Diagnostics.Mode != "question" {
			t.Fatalf("unexpected output: %+v err=%v", output, err)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("expected one retrieval call, got %d", inner.calls)
	}
}
