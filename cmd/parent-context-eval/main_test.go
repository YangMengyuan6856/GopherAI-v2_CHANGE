package main

import (
	"path/filepath"
	"testing"

	"GopherAI/internal/evaluation"
)

func TestLoadInputsUsesBalancedDatasetAndParentFixture(t *testing.T) {
	cases, fixture, err := loadInputs(
		filepath.Join("..", "..", "evals", "devsupport-parent-context-ab-v1.jsonl"),
		filepath.Join("..", "..", "evals", "fixtures", "kb-parent-context-fixture-v1.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != evaluation.ParentContextCaseCount || len(fixture.Chunks) < evaluation.ParentContextTargetCaseCount {
		t.Fatalf("unexpected dataset/fixture sizes: cases=%d chunks=%d", len(cases), len(fixture.Chunks))
	}
	parentLinked := 0
	for _, chunk := range fixture.Chunks {
		if chunk.ParentEvidenceID != "" && chunk.ParentContext != "" {
			parentLinked++
		}
	}
	if parentLinked != len(fixture.Chunks) {
		t.Fatalf("every authorized child must have parent context: linked=%d chunks=%d", parentLinked, len(fixture.Chunks))
	}
}
