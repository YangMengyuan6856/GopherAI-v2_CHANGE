package rcascoring

import (
	"testing"

	"GopherAI/internal/rcaexperiment"
)

func TestScoringDoesNotCreditRejectedKnownCases(t *testing.T) {
	answers, err := Answers()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, a := range answers {
		if seen[a.ID] {
			t.Fatal("duplicate scoring ID", a.ID)
		}
		seen[a.ID] = true
		s, err := Check(rcaexperiment.Diagnosis{CaseID: a.ID, Status: "insufficient_evidence", Candidates: []rcaexperiment.Candidate{{Service: a.Service, Fault: a.Fault}}})
		if err != nil || s.JointCorrect || s.ServiceTop1 || !s.Rejected || s.FalseAcceptance {
			t.Fatal(a.ID, s, err)
		}
		if !a.Supported {
			s, err = Check(rcaexperiment.Diagnosis{CaseID: a.ID, Status: "matched_hypothesis", Candidates: []rcaexperiment.Candidate{{Service: a.Service, Fault: "mem"}}})
			if err != nil || !s.FalseAcceptance || s.Rejected {
				t.Fatal("unsupported match not counted as false acceptance", s, err)
			}
		}
	}
	if len(seen) != 60 {
		t.Fatal("population changed", len(seen))
	}
}

func TestV4TruthIsHashBoundAndContainsOnlySupportedCases(t *testing.T) {
	d, err := rcaexperiment.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.SHA256) != 64 || len(d.PolicySHA256) != 64 {
		t.Fatal("dataset or policy is not hash bound")
	}
	answers, err := Answers()
	if err != nil || len(answers) != 60 {
		t.Fatal("unexpected truth population", len(answers), err)
	}
	counts := map[string]int{}
	for _, answer := range answers {
		if !answer.Supported || !rcaexperiment.KnownService(answer.Service) || rcaexperiment.FaultName(answer.Fault) == "" {
			t.Fatal("answer outside declared known-fault scope", answer)
		}
		counts[answer.Split]++
	}
	if counts["reference"] != 20 || counts["development"] != 20 || counts["holdout"] != 20 {
		t.Fatal("unexpected truth split", counts)
	}
}
