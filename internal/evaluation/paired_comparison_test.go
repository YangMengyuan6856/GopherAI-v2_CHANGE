package evaluation

import (
	"math"
	"reflect"
	"testing"
)

func TestAnalyzePairedObservationsReportsDenominatorsCIAndMcNemar(t *testing.T) {
	observations := make([]PairedObservation, 10)
	for index := range observations {
		observations[index] = PairedObservation{BaselineScore: .6, CandidateScore: 1}
		if index >= 8 {
			observations[index].CandidateScore = .6
		}
	}
	first, err := AnalyzePairedObservations(observations, .8)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AnalyzePairedObservations(observations, .8)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("paired analysis must be deterministic: first=%+v second=%+v", first, second)
	}
	if first.BaselineSuccess.Numerator != 0 || first.BaselineSuccess.Denominator != 10 || first.CandidateSuccess.Numerator != 8 || first.CandidateSuccess.Denominator != 10 {
		t.Fatalf("unexpected success fractions: %+v", first)
	}
	if first.Wins != 8 || first.Losses != 0 || first.Ties != 2 || first.DiscordantPairs != 8 {
		t.Fatalf("unexpected paired counts: %+v", first)
	}
	if math.Abs(first.McNemarExactTwoSidedPValue-.0078125) > 1e-12 || first.Conclusion != "candidate_better" {
		t.Fatalf("unexpected exact test: %+v", first)
	}
	if first.DeltaCI95Lower <= 0 || first.DeltaCI95Upper <= first.DeltaCI95Lower {
		t.Fatalf("unexpected bootstrap interval: %+v", first)
	}
}

func TestAnalyzePairedObservationsKeepsNoDifferenceInconclusive(t *testing.T) {
	result, err := AnalyzePairedObservations([]PairedObservation{{BaselineScore: .9, CandidateScore: .9}, {BaselineScore: 1, CandidateScore: 1}}, .8)
	if err != nil {
		t.Fatal(err)
	}
	if result.Conclusion != "inconclusive" || result.McNemarExactTwoSidedPValue != 1 || result.Ties != 2 || result.MeanDelta != 0 {
		t.Fatalf("no difference must remain inconclusive: %+v", result)
	}
}

func TestAnalyzePairedObservationsRejectsInvalidInputs(t *testing.T) {
	if _, err := AnalyzePairedObservations([]PairedObservation{{}, {}}, math.NaN()); err == nil {
		t.Fatal("expected invalid threshold error")
	}
	if _, err := AnalyzePairedObservations([]PairedObservation{{BaselineScore: -1}, {}}, .8); err == nil {
		t.Fatal("expected invalid score error")
	}
	if _, err := AnalyzePairedObservations([]PairedObservation{{}}, .8); err == nil {
		t.Fatal("expected minimum pair count error")
	}
}
