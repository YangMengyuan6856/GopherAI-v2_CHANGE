package intent

import (
	"GopherAI/internal/contract"
	"context"
	"errors"
	"testing"
)

type fakePatternStage struct {
	decision PatternDecision
	calls    int
}

func (stage *fakePatternStage) Recognize(PatternInput) PatternDecision {
	stage.calls++
	return stage.decision
}

type fakePrototypeStage struct {
	decision PrototypeDecision
	err      error
	calls    int
}

func (stage *fakePrototypeStage) Recognize(context.Context, string) (PrototypeDecision, error) {
	stage.calls++
	return stage.decision, stage.err
}

type fakeLLMStage struct {
	decision LLMDecision
	calls    int
}

func (stage *fakeLLMStage) Recognize(context.Context, LLMInput) LLMDecision {
	stage.calls++
	return stage.decision
}

func intentResult(label string, stage string, confidence float64) contract.IntentResult {
	return contract.IntentResult{Intent: label, Confidence: confidence, Version: stage,
		Stages: []contract.IntentStageResult{{Stage: stage, Intent: label, Confidence: confidence}}}
}

func TestCascadeShortCircuitsHighConfidencePattern(t *testing.T) {
	pattern := &fakePatternStage{decision: PatternDecision{Matched: true, Result: intentResult(Troubleshooting, "pattern", 0.98)}}
	prototype := new(fakePrototypeStage)
	llm := new(fakeLLMStage)
	recognizer, _ := NewCascadeRecognizer(pattern, prototype, llm)
	decision := recognizer.Recognize(context.Background(), CascadeInput{Question: "error"})
	if decision.Result.Intent != Troubleshooting || decision.Diagnostics.FinalStage != "pattern" || prototype.calls != 0 || llm.calls != 0 {
		t.Fatalf("cascade failed to short circuit: %+v", decision)
	}
}

func TestCascadeUsesPrototypeWithoutUnconditionalLLM(t *testing.T) {
	pattern := &fakePatternStage{decision: PatternDecision{Result: intentResult(General, "pattern", 0.4)}}
	prototype := &fakePrototypeStage{decision: PrototypeDecision{Matched: true, Result: intentResult(ProjectQA, "prototype", 0.92), Scores: []PrototypeScore{{Intent: ProjectQA, Score: 0.92}}, Margin: 0.2}}
	llm := new(fakeLLMStage)
	recognizer, _ := NewCascadeRecognizer(pattern, prototype, llm)
	decision := recognizer.Recognize(context.Background(), CascadeInput{Question: "semantic project question"})
	if decision.Result.Intent != ProjectQA || decision.Diagnostics.FinalStage != "prototype" || prototype.calls != 1 || llm.calls != 0 {
		t.Fatalf("cascade made unnecessary LLM call: %+v", decision)
	}
}

func TestCascadeSendsPatternConflictToLLM(t *testing.T) {
	patternResult := intentResult(Troubleshooting, "pattern", 0.79)
	patternResult.IsCompound = true
	pattern := &fakePatternStage{decision: PatternDecision{Result: patternResult, CandidateSet: []string{Troubleshooting, ToolTask}}}
	prototype := &fakePrototypeStage{decision: PrototypeDecision{Matched: true, Result: intentResult(Troubleshooting, "prototype", 0.91), Scores: []PrototypeScore{{Intent: Troubleshooting, Score: 0.91}}}}
	llm := &fakeLLMStage{decision: LLMDecision{Status: LLMStatusCompleted, Result: intentResult(Troubleshooting, "llm", 0.94)}}
	recognizer, _ := NewCascadeRecognizer(pattern, prototype, llm)
	decision := recognizer.Recognize(context.Background(), CascadeInput{Question: "compound"})
	if decision.Diagnostics.FinalStage != "llm" || llm.calls != 1 || len(decision.Result.Stages) != 3 {
		t.Fatalf("conflict did not reach fusion: %+v", decision)
	}
}

func TestCascadeDegradesToSafeClarification(t *testing.T) {
	pattern := &fakePatternStage{decision: PatternDecision{Result: intentResult(ToolTask, "pattern", 0.79), CandidateSet: []string{ToolTask, General}}}
	prototype := &fakePrototypeStage{err: errors.New("embedding down")}
	llm := &fakeLLMStage{decision: fallbackLLMDecision(LLMReasonTimeout)}
	recognizer, _ := NewCascadeRecognizer(pattern, prototype, llm)
	decision := recognizer.Recognize(context.Background(), CascadeInput{Question: "ambiguous operation"})
	if decision.Result.Intent != ToolTask || !decision.Result.NeedsClarify || decision.Result.Confidence > 0.59 || decision.Diagnostics.FinalStage != "degraded_clarification" {
		t.Fatalf("unsafe degraded decision: %+v", decision)
	}
}

func TestCascadeDoesNotAcceptFollowUpPrototypeWithoutContext(t *testing.T) {
	pattern := &fakePatternStage{decision: PatternDecision{Result: intentResult(General, "pattern", 0.4)}}
	prototype := &fakePrototypeStage{decision: PrototypeDecision{Matched: true, Result: intentResult(FollowUp, "prototype", 0.94), Scores: []PrototypeScore{{Intent: FollowUp, Score: 0.94}}}}
	llm := &fakeLLMStage{decision: fallbackLLMDecision(LLMReasonInvalidOutput)}
	recognizer, _ := NewCascadeRecognizer(pattern, prototype, llm)
	decision := recognizer.Recognize(context.Background(), CascadeInput{Question: "它呢"})
	if !decision.Diagnostics.LLMCalled || decision.Result.Intent != General || !decision.Result.NeedsClarify {
		t.Fatalf("context-free follow-up was accepted: %+v", decision)
	}
}
