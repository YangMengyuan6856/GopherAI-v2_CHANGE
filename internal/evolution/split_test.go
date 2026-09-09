package evolution

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSplitAuditFreezesDisjointDiagnosticPartitions(t *testing.T) {
	base := filepath.Join("..", "..", "evals")
	audit, err := LoadSplitAudit(filepath.Join(base, "devsupport-diagnostic-v1.jsonl"), filepath.Join(base, "devsupport-eval-v1.manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !audit.SourceHashVerified || audit.TotalCases != 40 || audit.CoveredCases != 40 || audit.OverlapCount != 0 || audit.DuplicateIDCount != 0 || len(audit.Splits) != 3 {
		t.Fatalf("unexpected split audit: %+v", audit)
	}
	if audit.Splits[0].CaseCount != 20 || audit.Splits[1].CaseCount != 10 || audit.Splits[2].CaseCount != 10 || audit.Splits[2].SealState != "sealed" || audit.Splits[2].CaseIDsExposed {
		t.Fatalf("unexpected split descriptors: %+v", audit.Splits)
	}
}

func TestSplitAccessDeniesLeakageAndReopen(t *testing.T) {
	if err := CheckSplitAccess(StageCandidateSearch, SplitEvolution, false, false); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(CheckSplitAccess(StageCandidateSearch, SplitValidation, false, false), ErrSplitAccessDenied) ||
		!errors.Is(CheckSplitAccess(StageCandidateSearch, SplitSealedHoldout, false, false), ErrSplitAccessDenied) ||
		!errors.Is(CheckSplitAccess(StageValidation, SplitValidation, false, false), ErrSplitAccessDenied) ||
		!errors.Is(CheckSplitAccess(StageFinalEvaluation, SplitSealedHoldout, true, true), ErrNewExperimentRequired) {
		t.Fatal("split access boundary was not fail-closed")
	}
}

func TestBuildSplitAuditRejectsDuplicateIDs(t *testing.T) {
	ids := make([]string, SplitTotalCases)
	for index := range ids {
		ids[index] = "case-" + strings.Repeat("0", 3-len(string(rune('0'+index%10)))) + string(rune('0'+index%10))
	}
	ids[39] = ids[0]
	if _, err := BuildSplitAudit(ids, strings.Repeat("a", 64), 1); !errors.Is(err, ErrEvolutionDatasetInvalid) {
		t.Fatalf("duplicate dataset was accepted: %v", err)
	}
}
