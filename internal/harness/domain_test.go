package harness

import (
	"errors"
	"testing"
	"time"
)

func TestRunStateTransitions(t *testing.T) {
	valid := [][2]State{{StateReceived, StateContextReady}, {StateContextReady, StatePlanned}, {StatePlanned, StateRunning}, {StateRunning, StateWaitingUser}, {StateWaitingUser, StateContextReady}, {StateRunning, StateSucceeded}}
	for _, item := range valid {
		if err := ValidateTransition(item[0], item[1]); err != nil {
			t.Fatalf("expected %s -> %s to be valid: %v", item[0], item[1], err)
		}
	}
	invalid := [][2]State{{StateReceived, StateSucceeded}, {StateWaitingUser, StateRunning}, {StateSucceeded, StateRunning}, {StateCancelled, StateContextReady}}
	for _, item := range invalid {
		if err := ValidateTransition(item[0], item[1]); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("expected %s -> %s to be rejected, got %v", item[0], item[1], err)
		}
	}
}

func TestBudgetRejectsAnyExceededDimension(t *testing.T) {
	budget := DefaultBudget()
	budget.UsedToolCalls = budget.MaxToolCalls + 1
	if err := budget.Validate(); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected budget exceeded, got %v", err)
	}
}

func TestPublicStepRejectsHiddenUnboundedPayloadShape(t *testing.T) {
	step := PublicStep{StepID: "context-1", Attempt: 1, Kind: "context_assemble", Status: "completed", PublicSummary: string(make([]rune, 501)), StateVersion: 2, StartedAt: time.Now()}
	if err := step.Validate(); err == nil {
		t.Fatal("expected oversized public summary to be rejected")
	}
}
