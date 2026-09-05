package rag

import (
	"GopherAI/internal/contract"
	"testing"
)

func TestEvidenceGateAcceptsStrongCrossRetrieverEvidence(t *testing.T) {
	result := DefaultEvidenceGate().Evaluate(SearchOutput{
		Hits:        []SearchHit{{Evidence: contract.Evidence{Score: 0.95, Retrieval: "dense+bm25"}}},
		Diagnostics: SearchDiagnostics{DenseCandidates: 2, KeywordCandidates: 1},
	})
	if !result.Accepted || result.ReasonCode != GateReasonSufficient || result.HybridEvidenceCount != 1 {
		t.Fatalf("expected sufficient evidence, got %+v", result)
	}
}

func TestEvidenceGateRejectsDenseOnlyEvidenceWithoutCallingItSufficient(t *testing.T) {
	result := DefaultEvidenceGate().Evaluate(SearchOutput{
		Hits:        []SearchHit{{Evidence: contract.Evidence{Score: 0.99, Retrieval: "dense"}}},
		Diagnostics: SearchDiagnostics{DenseCandidates: 3},
	})
	if result.Accepted || result.ReasonCode != GateReasonNoHybridSupport || len(result.FollowUpQuestions) == 0 {
		t.Fatalf("expected hybrid coverage rejection, got %+v", result)
	}
}

func TestEvidenceGateAcceptsDenseEvidenceWithIndependentLexicalSupport(t *testing.T) {
	result := DefaultEvidenceGate().Evaluate(SearchOutput{
		Hits:        []SearchHit{{Evidence: contract.Evidence{Score: 0.5, Retrieval: "dense"}}},
		Diagnostics: SearchDiagnostics{DenseCandidates: 3, LexicalCandidates: 1},
	})
	if !result.Accepted || result.ReasonCode != GateReasonDenseLexical || result.LexicalEvidenceCount != 1 {
		t.Fatalf("expected dense lexical acceptance, got %+v", result)
	}
}

func TestEvidenceGateRejectsEmptyAndLowScoreEvidence(t *testing.T) {
	empty := DefaultEvidenceGate().Evaluate(SearchOutput{})
	if empty.Accepted || empty.ReasonCode != GateReasonNoEvidence {
		t.Fatalf("expected empty evidence rejection, got %+v", empty)
	}
	low := DefaultEvidenceGate().Evaluate(SearchOutput{
		Hits:        []SearchHit{{Evidence: contract.Evidence{Score: 0.6, Retrieval: "dense+bm25"}}},
		Diagnostics: SearchDiagnostics{DenseCandidates: 1, KeywordCandidates: 1},
	})
	if low.Accepted || low.ReasonCode != GateReasonLowConfidence {
		t.Fatalf("expected low-confidence rejection, got %+v", low)
	}
}

func TestEvidenceGateRejectsEffectiveSourceConflictBeforeGeneration(t *testing.T) {
	output := SearchOutput{
		Hits:        []SearchHit{{Evidence: contract.Evidence{ID: "e1", Score: .98, Retrieval: "dense+bm25"}}},
		Diagnostics: SearchDiagnostics{DenseCandidates: 1, KeywordCandidates: 1},
		Conflicts:   []contract.EvidenceConflict{{ConflictID: "conflict-1", FactKey: "release > timeout_seconds"}},
	}
	result := DefaultEvidenceGate().Evaluate(output)
	if result.Accepted || result.ReasonCode != GateReasonEvidenceConflict || result.ConflictCount != 1 || len(result.FollowUpQuestions) != 1 {
		t.Fatalf("effective conflicts must stop generation: %+v", result)
	}
}
