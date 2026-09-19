package main

import (
	"testing"

	"GopherAI/internal/rcaexperiment"
)

func TestPublishedHoldoutCannotBeRegeneratedAsAFreshEvaluation(t *testing.T) {
	d, err := rcaexperiment.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := reportKind("holdout", d.SHA256, "../../evals/rcaeval/agent-evaluation.json"); got != "previously_exposed_case_replay" {
		t.Fatalf("holdout rerun kind = %q", got)
	}
	if got := reportKind("holdout", "new-expanded-dataset", "../../evals/rcaeval/agent-evaluation.json"); got != "fixed_known_fault_evaluation" {
		t.Fatalf("new dataset kind = %q", got)
	}
	if got := reportKind("development", "any", "../../evals/rcaeval/agent-evaluation.json"); got != "development_iteration" {
		t.Fatalf("development kind = %q", got)
	}
}
