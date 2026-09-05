package rag

import (
	"context"
	"testing"

	"GopherAI/internal/contract"
)

type parentSearchStub struct {
	output SearchOutput
	input  SearchInput
}

func (stub *parentSearchStub) Search(_ context.Context, input SearchInput) (SearchOutput, error) {
	stub.input = input
	return stub.output, nil
}

func TestParentContextRetrieverBoundsParentAndDocumentOccupancy(t *testing.T) {
	base := &parentSearchStub{output: SearchOutput{Hits: []SearchHit{
		parentHit("c1", "d1", "p1", "parent one"),
		parentHit("c2", "d1", "p1", "parent one"),
		parentHit("c3", "d1", "p1", "parent one"),
		parentHit("c4", "d1", "p2", "parent two"),
		parentHit("c5", "d1", "p3", "parent three"),
		parentHit("c6", "d2", "", ""),
	}}}
	retriever, err := NewParentContextRetriever(base)
	if err != nil {
		t.Fatal(err)
	}

	output, err := retriever.Search(context.Background(), SearchInput{TenantID: "tenant", UserID: "user", Query: "compare", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if base.input.TopK != MaxTopK || len(output.Hits) != 4 {
		t.Fatalf("parent strategy must over-fetch then apply bounded selection: input=%+v output=%+v", base.input, output)
	}
	if output.Hits[0].Evidence.ID != "c1" || output.Hits[1].Evidence.ID != "c2" || output.Hits[2].Evidence.ID != "c4" || output.Hits[3].Evidence.ID != "c6" {
		t.Fatalf("child evidence order or exact citation identity changed: %+v", output.Hits)
	}
	diagnostics := output.Diagnostics.Parent
	if diagnostics == nil || diagnostics.FilteredByParent != 1 || diagnostics.FilteredByDocument != 1 || diagnostics.ParentContextHits != 3 || !diagnostics.ChildCitationOnly {
		t.Fatalf("unexpected parent-context diagnostics: %+v", diagnostics)
	}
}

func parentHit(id, documentID, parentID, parentContext string) SearchHit {
	return SearchHit{Evidence: contract.Evidence{
		ID: id, TenantID: "tenant", SourceID: documentID, ParentEvidenceID: parentID,
		Content: "precise child " + id, ParentContext: parentContext,
	}}
}
