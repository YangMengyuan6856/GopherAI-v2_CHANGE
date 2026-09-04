package intentplatform

import (
	"GopherAI/internal/contract"
	intentdomain "GopherAI/internal/intent"
	"context"
	"errors"
	"testing"
	"time"
)

type fakePreviousResolver struct {
	intent string
	err    error
	calls  int
}

func (resolver *fakePreviousResolver) Resolve(context.Context, string, string) (string, error) {
	resolver.calls++
	return resolver.intent, resolver.err
}

type capturingCascade struct {
	input intentdomain.CascadeInput
}

func (cascade *capturingCascade) Recognize(_ context.Context, input intentdomain.CascadeInput) intentdomain.CascadeDecision {
	cascade.input = input
	return intentdomain.CascadeDecision{
		Result:      contract.IntentResult{Intent: intentdomain.FollowUp, Confidence: .95, Version: intentdomain.CascadeVersion},
		Diagnostics: intentdomain.CascadeDiagnostics{FinalStage: "pattern"},
	}
}

func TestRuntimeRecognizerResolvesPreviousSessionIntentAndCachesCascade(t *testing.T) {
	resolver := &fakePreviousResolver{intent: intentdomain.Troubleshooting}
	cascade := new(capturingCascade)
	factoryCalls := 0
	recognizer := newRuntimeRecognizer(resolver, func() (runtimeCascade, error) {
		factoryCalls++
		return cascade, nil
	}, time.Second)

	input := intentdomain.CascadeInput{Question: "那第二种原因怎么验证？", UserID: "user", SessionID: "session-1"}
	first := recognizer.Recognize(context.Background(), input)
	second := recognizer.Recognize(context.Background(), input)
	if first.Result.Intent != intentdomain.FollowUp || second.Result.Intent != intentdomain.FollowUp {
		t.Fatalf("unexpected decisions: first=%+v second=%+v", first, second)
	}
	if cascade.input.PreviousIntent != intentdomain.Troubleshooting || resolver.calls != 2 {
		t.Fatalf("previous intent not supplied: input=%+v resolver_calls=%d", cascade.input, resolver.calls)
	}
	if factoryCalls != 1 {
		t.Fatalf("cascade should be initialized once, got %d", factoryCalls)
	}
}

func TestRuntimeRecognizerBacksOffFactoryFailureAndReturnsSafeDecision(t *testing.T) {
	factoryCalls := 0
	recognizer := newRuntimeRecognizer(nil, func() (runtimeCascade, error) {
		factoryCalls++
		return nil, errors.New("private model endpoint")
	}, time.Hour)

	for range 2 {
		decision := recognizer.Recognize(context.Background(), intentdomain.CascadeInput{Question: "question"})
		if decision.Result.Intent != intentdomain.General || !decision.Result.NeedsClarify || decision.Diagnostics.FinalStage != "unavailable" {
			t.Fatalf("failure was not safely degraded: %+v", decision)
		}
	}
	if factoryCalls != 1 {
		t.Fatalf("factory failure should be backed off, got %d calls", factoryCalls)
	}
}

func TestRuntimeRecognizerRecordsPreviousIntentLookupFailureWithoutBlocking(t *testing.T) {
	resolver := &fakePreviousResolver{err: errors.New("database unavailable")}
	cascade := new(capturingCascade)
	recognizer := newRuntimeRecognizer(resolver, func() (runtimeCascade, error) { return cascade, nil }, time.Second)
	decision := recognizer.Recognize(context.Background(), intentdomain.CascadeInput{Question: "question", SessionID: "session-1"})
	if len(decision.Diagnostics.FallbackReasons) != 1 || decision.Diagnostics.FallbackReasons[0] != "previous_intent_unavailable" {
		t.Fatalf("lookup fallback not observable: %+v", decision.Diagnostics)
	}
}
