package rag

import (
	"testing"

	"GopherAI/internal/contract"
)

func TestDetectEvidenceConflictsKeepsBothEffectiveSources(t *testing.T) {
	hits := []SearchHit{
		{Evidence: contract.Evidence{ID: "e1", SourceID: "json-doc", SourceVersion: "2", SourceRevision: "rev-a", Section: "release", Content: `"timeout_seconds": 47,`, Authority: 50}},
		{Evidence: contract.Evidence{ID: "e2", SourceID: "yaml-doc", SourceVersion: "4", SourceRevision: "rev-b", Section: "release", Content: "timeout_seconds: 60", Authority: 80}},
	}
	conflicts := DetectEvidenceConflicts(hits)
	if len(conflicts) != 1 || conflicts[0].FactKey != "release > timeout_seconds" || conflicts[0].Status != EvidenceConflictStatusReview || len(conflicts[0].Values) != 2 {
		t.Fatalf("unexpected structured conflict: %+v", conflicts)
	}
	if conflicts[0].Values[0].Value != "47" || conflicts[0].Values[1].Value != "60" {
		t.Fatalf("conflict silently selected or lost a value: %+v", conflicts[0])
	}
}

func TestDetectEvidenceConflictsIgnoresSameValueAndSameSource(t *testing.T) {
	tests := []struct {
		name string
		hits []SearchHit
	}{
		{name: "same value", hits: []SearchHit{
			{Evidence: contract.Evidence{ID: "e1", SourceID: "a", Section: "service", Content: "max_attempts: 6"}},
			{Evidence: contract.Evidence{ID: "e2", SourceID: "b", Section: "service", Content: "max_attempts: 6"}},
		}},
		{name: "same source", hits: []SearchHit{
			{Evidence: contract.Evidence{ID: "e1", SourceID: "a", SourceVersion: "1", Section: "service", Content: "max_attempts: 6"}},
			{Evidence: contract.Evidence{ID: "e2", SourceID: "a", SourceVersion: "1", Section: "service", Content: "max_attempts: 7"}},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if conflicts := DetectEvidenceConflicts(test.hits); len(conflicts) != 0 {
				t.Fatalf("false conflict: %+v", conflicts)
			}
		})
	}
}
