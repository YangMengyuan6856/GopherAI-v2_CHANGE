package main

import (
	"os"
	"path/filepath"
	"testing"

	"GopherAI/internal/rcaexperiment"
)

func TestPublishedHoldoutCannotBeRegeneratedAsAFreshEvaluation(t *testing.T) {
	d, err := rcaexperiment.Load()
	if err != nil {
		t.Fatal(err)
	}
	published := filepath.Join(t.TempDir(), "published.json")
	if err := os.WriteFile(published, []byte(`{"dataset_sha256":"`+d.SHA256+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := reportKind("holdout", d.SHA256, published); got != "previously_exposed_case_replay" {
		t.Fatalf("holdout rerun kind = %q", got)
	}
	if got := reportKind("holdout", "new-expanded-dataset", published); got != "fixed_known_fault_evaluation" {
		t.Fatalf("new dataset kind = %q", got)
	}
	if got := reportKind("development", "any", published); got != "development_iteration" {
		t.Fatalf("development kind = %q", got)
	}
}
