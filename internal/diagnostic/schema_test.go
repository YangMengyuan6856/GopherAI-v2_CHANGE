package diagnostic

import (
	"errors"
	"testing"
)

func validResult() Result {
	return Result{
		Version: SchemaVersion, Symptom: "Backend returns 503 while readiness remains healthy.",
		Components: []string{"backend", "reverse_proxy"}, ErrorSignatures: []string{"HTTP_503"},
		KnownEnvironment: []EnvironmentFact{{Key: "deployment", Value: "docker", Source: "user_message:1", Confidence: 1}},
		Hypotheses: []Hypothesis{{
			ID: "H1", Cause: "The proxy upstream does not match the backend listener.", Confidence: .75,
			Rationale:         "The backend readiness observation conflicts with the public 503 response.",
			Evidence:          []EvidenceReference{{ID: "OBS1", SourceType: EvidenceUserObservation, Summary: "Readiness returned HTTP 200."}},
			VerificationSteps: []VerificationStep{{ID: "V1", ActionType: ActionCompare, Instruction: "Compare the configured upstream and listener ports.", ExpectedObservation: "Both ports are equal.", FailureMeaning: "A mismatch supports H1.", ReadOnly: true}},
		}},
		ConclusionStatus: ConclusionHypothesis,
	}
}

func TestDiagnosticResultAcceptsBoundedEvidenceBasedHypotheses(t *testing.T) {
	if err := validResult().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDiagnosticResultRejectsPrematureConfirmation(t *testing.T) {
	result := validResult()
	result.ConclusionStatus = ConclusionConfirmed
	if err := result.Validate(); !errors.Is(err, ErrInvalidDiagnostic) {
		t.Fatalf("unreferenced confirmation must fail: %v", err)
	}
	result.ConfirmedRootCauseID = "H1"
	if err := result.Validate(); err != nil {
		t.Fatalf("referenced confirmation should pass: %v", err)
	}
}

func TestDiagnosticResultRequiresExplicitQuestionWhenEvidenceIsInsufficient(t *testing.T) {
	result := validResult()
	result.Hypotheses = nil
	result.ConclusionStatus = ConclusionInsufficient
	result.NeedsUserInput = true
	if err := result.Validate(); !errors.Is(err, ErrInvalidDiagnostic) {
		t.Fatalf("insufficient result without a question must fail: %v", err)
	}
	result.MissingInformation = []MissingInformation{{Field: "backend_log", Question: "请提供同一 Trace 的后端错误日志。", Critical: true}}
	if err := result.Validate(); err != nil {
		t.Fatalf("bounded clarification result should pass: %v", err)
	}
}

func TestDiagnosticResultRejectsWriteVerificationAndUnorderedHypotheses(t *testing.T) {
	result := validResult()
	result.Hypotheses[0].VerificationSteps[0].ReadOnly = false
	if err := result.Validate(); !errors.Is(err, ErrInvalidDiagnostic) {
		t.Fatalf("write verification must fail: %v", err)
	}
	result = validResult()
	second := result.Hypotheses[0]
	second.ID = "H2"
	second.Confidence = .9
	result.Hypotheses = append(result.Hypotheses, second)
	if err := result.Validate(); !errors.Is(err, ErrInvalidDiagnostic) {
		t.Fatalf("unordered hypotheses must fail: %v", err)
	}
}

func TestDiagnosticResultEnforcesThreeHypothesisBudget(t *testing.T) {
	result := validResult()
	for index := 2; index <= 4; index++ {
		hypothesis := result.Hypotheses[0]
		hypothesis.ID = string(rune('H' + index - 1))
		hypothesis.Confidence = float64(10-index) / 10
		result.Hypotheses = append(result.Hypotheses, hypothesis)
	}
	if err := result.Validate(); !errors.Is(err, ErrInvalidDiagnostic) {
		t.Fatalf("more than three hypotheses must fail: %v", err)
	}
}
