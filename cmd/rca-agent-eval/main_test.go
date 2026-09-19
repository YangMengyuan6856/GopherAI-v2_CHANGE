package main

import "testing"

func TestPublishedHoldoutCannotBeRegeneratedAsAFreshEvaluation(t *testing.T) {
	if got := reportKind("holdout"); got != "previously_exposed_case_replay" {
		t.Fatalf("holdout rerun kind = %q", got)
	}
	if got := reportKind("development"); got != "development_iteration" {
		t.Fatalf("development kind = %q", got)
	}
}
