package rcascoring

import (
	"context"
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
	if len(seen) != 27 {
		t.Fatal("population changed", len(seen))
	}
}

func TestFrozenReportBindingAndDenominators(t *testing.T) {
	d, err := rcaexperiment.Load()
	if err != nil {
		t.Fatal(err)
	}
	if d.SHA256 != "1e10d5a275e2942b07dac5483b299cbf4f0e444a051a1373db048c0084281a30" || d.PolicySHA256 != "dd602b07b958dbcdfc01cbf6b6c0b0770f423c8b0f352360a09056efa403a336" {
		t.Fatal("frozen artifact bytes changed; do not silently reuse the report")
	}
	r, err := Evaluate(context.Background(), d, "holdout")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Cases) != 36 {
		t.Fatal("missing evaluation runs", len(r.Cases))
	}
	for strategy, m := range r.Metrics {
		if m.Supported != 6 || m.Unsupported != 6 || m.Errors != 0 || m.UnknownRejected+m.FalseAcceptance != 6 {
			t.Fatal(strategy, m)
		}
	}
	if _, err = Evaluate(context.Background(), d, "reference"); err == nil {
		t.Fatal("reference cases must not be counted as tests")
	}
}
