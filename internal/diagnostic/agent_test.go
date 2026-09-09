package diagnostic

import "testing"

func TestAgentReturnsBoundedEvidenceBackedHypotheses(t *testing.T) {
	_, result, err := NewAgent().Analyze("Docker 容器内访问 redis-vector:6379 connection refused，代理随后返回 HTTP 502 Bad Gateway")
	if err != nil {
		t.Fatal(err)
	}
	if result.ConclusionStatus != ConclusionHypothesis || len(result.Hypotheses) != 2 {
		t.Fatalf("unexpected result: %#v", result)
	}
	for _, hypothesis := range result.Hypotheses {
		if len(hypothesis.Evidence) == 0 || len(hypothesis.VerificationSteps) == 0 {
			t.Fatalf("hypothesis lacks evidence or verification: %#v", hypothesis)
		}
		for _, step := range hypothesis.VerificationSteps {
			if !step.ReadOnly {
				t.Fatalf("agent proposed a mutating verification: %#v", step)
			}
		}
	}
}

func TestAgentAsksForCriticalEvidenceInsteadOfInventingRootCause(t *testing.T) {
	_, result, err := NewAgent().Analyze("系统偶尔不可用，请帮我看看")
	if err != nil {
		t.Fatal(err)
	}
	if result.ConclusionStatus != ConclusionInsufficient || !result.NeedsUserInput || len(result.Hypotheses) != 0 || len(result.MissingInformation) < 2 {
		t.Fatalf("agent should ask for evidence: %#v", result)
	}
}

func TestAgentNeverConfirmsFromObservationAlone(t *testing.T) {
	_, result, err := NewAgent().Analyze("container OOMKilled=true exit code 137")
	if err != nil {
		t.Fatal(err)
	}
	if result.ConclusionStatus == ConclusionConfirmed || result.ConfirmedRootCauseID != "" {
		t.Fatalf("unverified observation was promoted to confirmed root cause: %#v", result)
	}
}
